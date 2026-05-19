package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIRFromValue_Primitives(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string // Raw of the resulting Literal
	}{
		{"int", 42, "42"},
		{"float", 3.14, "3.14"},
		{"string", "hi", "hi"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := irFromValue(tt.val, nil)
			lit, ok := got.(*ir.Literal)
			if !ok {
				t.Fatalf("got %T, want *ir.Literal", got)
			}
			if lit.Raw != tt.want {
				t.Errorf("Raw = %q, want %q", lit.Raw, tt.want)
			}
		})
	}
}

func TestIRFromValue_Map(t *testing.T) {
	val := map[string]any{
		"r": 255,
		"g": 128,
		"b": 64,
		"a": 255,
	}
	got := irFromValue(val, nil)
	sl, ok := got.(*ir.StructLit)
	if !ok {
		t.Fatalf("got %T, want *ir.StructLit", got)
	}
	if len(sl.Fields) != 4 {
		t.Fatalf("Fields count = %d, want 4", len(sl.Fields))
	}
	for _, f := range sl.Fields {
		if f.Name == "r" {
			lit := f.Value.(*ir.Literal)
			if lit.Raw != "255" {
				t.Errorf("r = %q", lit.Raw)
			}
		}
	}
}

func TestIRFromValue_Slice(t *testing.T) {
	val := []any{1, 2, 3}
	got := irFromValue(val, nil)
	ll, ok := got.(*ir.ListLit)
	if !ok {
		t.Fatalf("got %T, want *ir.ListLit", got)
	}
	if len(ll.Elems) != 3 {
		t.Fatalf("Elems = %d, want 3", len(ll.Elems))
	}
}

func TestIRFromValue_Nil(t *testing.T) {
	got := irFromValue(nil, nil)
	lit, ok := got.(*ir.Literal)
	if !ok || lit.Raw != "null" {
		t.Errorf("got %v, want null Literal", got)
	}
}
