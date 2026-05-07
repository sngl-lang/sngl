package golang

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestExportName(t *testing.T) {
	tr := &Translator{}
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"count", "Count"},
		{"myField", "MyField"},
	}
	for _, tt := range tests {
		got := tr.ExportName(tt.in)
		if got != tt.want {
			t.Errorf("ExportName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIRTypeToGo_TypeRef(t *testing.T) {
	refInt := &ir.Type{Kind: ir.TypeRef, Elems: []*ir.Type{{Kind: ir.TypeInt}}}
	if got := IRTypeToGo(refInt); got != "*int" {
		t.Errorf("TypeRef<int> = %q, want %q", got, "*int")
	}

	// ref with no elems falls back to unsafe.Pointer
	refEmpty := &ir.Type{Kind: ir.TypeRef}
	if got := IRTypeToGo(refEmpty); got != "unsafe.Pointer" {
		t.Errorf("TypeRef<> = %q, want %q", got, "unsafe.Pointer")
	}
}
