package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestExtractBindingsEmitsPropBinding(t *testing.T) {
	comp := &ir.Component{
		Name:  "Stepper",
		Props: []*ir.Prop{{Name: "count", Type: ir.TypInt, Bidirectional: true}},
	}
	target := &ir.Ident{Name: "steps", Type: ir.TypInt}
	props := []ir.Arg{{Name: ":count", Value: target}}

	c := &checker{}
	outProps, outHandlers, bindings := c.extractBindings(comp, props, nil)

	if len(outProps) != 1 || outProps[0].Name != "count" {
		t.Fatalf("prop name not stripped: %v", outProps)
	}
	if len(outHandlers) != 0 {
		t.Fatalf("expected no synthesized handlers, got %d", len(outHandlers))
	}
	if len(bindings) != 1 || bindings[0].PropName != "count" {
		t.Fatalf("expected binding for count, got %v", bindings)
	}
	if ident, ok := bindings[0].Target.(*ir.Ident); !ok || ident.Name != "steps" {
		t.Fatalf("binding target wrong: %v", bindings[0].Target)
	}
}
