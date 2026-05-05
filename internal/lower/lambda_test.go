package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestAnalyzeCapturesImmutableRead(t *testing.T) {
	// Lambda body reads outer `n` once; n is read-only inside the body.
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt}},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 1 {
		t.Fatalf("want 1 capture, got %d", len(caps))
	}
	if caps[0].Sym != outerN {
		t.Errorf("captured wrong sym")
	}
	if caps[0].Mutable {
		t.Errorf("read-only capture marked mutable")
	}
}

func TestAnalyzeCapturesMutableWrite(t *testing.T) {
	outerN := &ir.Var{Name: "n", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Assign{
			Target: &ir.Ident{Name: "n", Sym: outerN, Type: ir.TypInt},
			Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
		},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 1 || !caps[0].Mutable {
		t.Errorf("expected mutable capture; got %+v", caps)
	}
}

func TestAnalyzeCapturesParamNotCaptured(t *testing.T) {
	p := &ir.Param{Name: "x", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Ident{Name: "x", Sym: p, Type: ir.TypInt}},
	}
	caps := analyzeCaptures(body, []*ir.Param{p})
	if len(caps) != 0 {
		t.Errorf("param should not be captured: %+v", caps)
	}
}

func TestAnalyzeCapturesOrderStable(t *testing.T) {
	a := &ir.Var{Name: "a", Type: ir.TypInt}
	b := &ir.Var{Name: "b", Type: ir.TypInt}
	body := []ir.Stmt{
		&ir.Return{Value: &ir.Binary{
			Op:    0, // any
			Left:  &ir.Ident{Name: "a", Sym: a, Type: ir.TypInt},
			Right: &ir.Ident{Name: "b", Sym: b, Type: ir.TypInt},
			Type:  ir.TypInt,
		}},
	}
	caps := analyzeCaptures(body, nil)
	if len(caps) != 2 || caps[0].Sym != a || caps[1].Sym != b {
		t.Errorf("order unstable: %+v", caps)
	}
}
