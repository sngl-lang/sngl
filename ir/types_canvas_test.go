package ir_test

import (
	"testing"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestTypeShape(t *testing.T) {
	if ir.TypShape == nil {
		t.Fatal("TypShape must not be nil")
	}
	if ir.TypShape.Kind != ir.TypeShape {
		t.Errorf("TypShape.Kind = %v, want TypeShape", ir.TypShape.Kind)
	}
}
