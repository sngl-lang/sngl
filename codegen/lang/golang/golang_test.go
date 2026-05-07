package golang

import (
	"strings"
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

func TestEmitCHeader_Basic(t *testing.T) {
	tr := &Translator{}
	ni := &ir.NativeImport{
		ImportPath: "c:///usr/include/test.h",
		LinkFlags:  []string{"-ltest"},
	}
	got := tr.EmitCHeader([]*ir.NativeImport{ni})
	if !strings.Contains(got, `import "C"`) {
		t.Errorf("EmitCHeader missing import \"C\"; got:\n%s", got)
	}
	if !strings.Contains(got, `#include "/usr/include/test.h"`) {
		t.Errorf("EmitCHeader missing #include; got:\n%s", got)
	}
	if !strings.Contains(got, "#cgo LDFLAGS: -ltest") {
		t.Errorf("EmitCHeader missing #cgo LDFLAGS; got:\n%s", got)
	}
}

func TestEmitCHeader_Empty(t *testing.T) {
	tr := &Translator{}
	got := tr.EmitCHeader(nil)
	if got != "" {
		t.Errorf("EmitCHeader(nil) = %q, want empty", got)
	}
}
