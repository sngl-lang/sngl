package ir_test

import (
	"testing"

	"duckfam.us/sngl/ir"
)

func TestPropBindingFields(t *testing.T) {
	b := ir.PropBinding{PropName: "count", Target: &ir.Ident{Name: "steps"}}
	if b.PropName != "count" {
		t.Fatalf("PropName = %q, want count", b.PropName)
	}
	n := &ir.NodeInst{}
	n.Bindings = append(n.Bindings, b)
	if len(n.Bindings) != 1 {
		t.Fatalf("len(Bindings) = %d, want 1", len(n.Bindings))
	}
}
