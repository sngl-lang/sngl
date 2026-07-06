package ir_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestValidateCatchesUnresolvedIdentAndNilCallType(t *testing.T) {
	// A func body referencing an unresolved ident and calling with nil Type.
	fn := &ir.Func{
		Name: "f",
		Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "x", Sym: nil}, // unresolved
				Value:  &ir.Call{Func: &ir.Func{Name: "g"}, Type: nil},
			},
		},
	}
	pkg := &ir.Package{Funcs: []*ir.Func{fn}}

	errs := ir.Validate(pkg)
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "unresolved identifier \"x\"") {
		t.Errorf("expected unresolved-ident violation, got:\n%s", joined)
	}
	if !strings.Contains(joined, "nil return Type") {
		t.Errorf("expected nil-call-Type violation, got:\n%s", joined)
	}
}

func TestValidateAcceptsSynthesizedIdent(t *testing.T) {
	fn := &ir.Func{
		Name: "f",
		Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "__n0", Synthesized: true}, // ok: pass-synthesized
				Value:  &ir.Literal{Raw: "1"},
			},
		},
	}
	pkg := &ir.Package{Funcs: []*ir.Func{fn}}
	if errs := ir.Validate(pkg); len(errs) != 0 {
		t.Errorf("synthesized ident should not violate; got %v", errs)
	}
}
