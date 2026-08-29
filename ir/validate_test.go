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
	var joined strings.Builder
	for _, e := range errs {
		joined.WriteString(e.Error() + "\n")
	}
	if !strings.Contains(joined.String(), "unresolved identifier \"x\"") {
		t.Errorf("expected unresolved-ident violation, got:\n%s", joined.String())
	}
	if !strings.Contains(joined.String(), "nil return Type") {
		t.Errorf("expected nil-call-Type violation, got:\n%s", joined.String())
	}
}

func TestValidateAcceptsSynthesizedIdentWithSym(t *testing.T) {
	slot := &ir.Var{Name: "__slot0", Type: ir.ListOf(ir.TypDyn), Synthesized: true}
	fn := &ir.Func{
		Name: "f",
		Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "__slot0", Sym: slot, Synthesized: true},
				Value:  &ir.Literal{Value: "1"},
			},
			// Element refs name a node in the emitted tree, not a
			// declaration, so they carry no symbol.
			&ir.Assign{
				Target: &ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true},
				Value:  &ir.Literal{Value: "1"},
			},
		},
	}
	pkg := &ir.Package{Funcs: []*ir.Func{fn}}
	if errs := ir.Validate(pkg); len(errs) != 0 {
		t.Errorf("synthesized ident should not violate; got %v", errs)
	}
}

func TestValidateCatchesSynthesizedIdentWithoutSym(t *testing.T) {
	fn := &ir.Func{
		Name: "f",
		Block: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "__slot0", Synthesized: true},
				Value:  &ir.Literal{Value: "1"},
			},
		},
	}
	pkg := &ir.Package{Funcs: []*ir.Func{fn}}
	errs := ir.Validate(pkg)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `unresolved identifier "__slot0"`) {
		t.Errorf("a synthesized ident with no Sym must violate; got %v", errs)
	}
}
