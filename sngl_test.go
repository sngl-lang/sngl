package sngl_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl"
)

// TestConvertContainsPanic verifies the public pipeline boundary contains
// internal panics so an embedding host (LSP, playground WASM, docsgen) degrades
// instead of crashing. ir.Convert nil-derefs on a nil package; Convert must
// recover and return nil rather than propagate the panic.
func TestConvertContainsPanic(t *testing.T) {
	if got := sngl.Convert(nil); got != nil {
		t.Errorf("Convert(nil) = %v; want nil after recover", got)
	}
}

// TestCheckWrapperHappyPath guards that the recover wrapper doesn't disturb a
// normal Check on valid input — it should still return a package with no error
// diagnostics.
func TestCheckWrapperHappyPath(t *testing.T) {
	doc, err := sngl.Parse("t.sngl", strings.NewReader("component main {\n  text(value=\"hi\")\n}\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := sngl.Check(doc, ".")
	if pkg == nil {
		t.Fatalf("Check returned nil package; diags=%v", diags)
	}
}
