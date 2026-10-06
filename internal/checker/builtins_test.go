package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// The macro stamps a kind on any declaration form that can carry one; whether
// that kind belongs on that form is decided here, where the reference is
// stored, because that is where the requirement comes from.
func TestBindBuiltinRejectsWrongDeclarationForm(t *testing.T) {
	c := &checker{}
	sd := &ir.StructDef{Name: "context"}
	var dst *ir.Component
	if got := bindBuiltin(c, &dst, ir.BuiltinContext, sd); got != nil {
		t.Errorf("bound a struct to a component field")
	}
	if dst != nil {
		t.Errorf("destination written despite the mismatch")
	}
	if len(c.diags) != 1 || !strings.Contains(c.diags[0].Msg, "requires a") {
		t.Fatalf("diags = %+v, want one 'requires a' error", c.diags)
	}
}

// A kind classifies one declaration. Two carrying it would be two
// incompatible things rather than aliases, so the second is an error and not a
// silent overwrite.
func TestBindBuiltinRejectsDuplicate(t *testing.T) {
	c := &checker{}
	first := &ir.Component{Name: "context"}
	second := &ir.Component{Name: "alsoContext"}
	var dst *ir.Component
	bindBuiltin(c, &dst, ir.BuiltinContext, first)
	bindBuiltin(c, &dst, ir.BuiltinContext, second)
	if dst != first {
		t.Errorf("dst = %v, want the first declaration to win", dst)
	}
	if len(c.diags) != 1 || !strings.Contains(c.diags[0].Msg, "more than once") {
		t.Fatalf("diags = %+v, want one duplicate error", c.diags)
	}
}
