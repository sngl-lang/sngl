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

// TestMarkedFieldWithoutMacroPass guards the route into the compiler the LSP
// and the playground take: sngl.Parse + sngl.Check, with nothing between them.
// A marked field has to be a field there too — while a marked declaration was
// a wrapper around itself, an editor reported the field as unknown on every
// struct that used one.
func TestMarkedFieldWithoutMacroPass(t *testing.T) {
	const src = `import . "sngl:std"

struct Entry {
    #[foreign("Title")]
    title string
}

component main {
    text(value=Entry{title = "hi"}.title)
}
`
	doc, err := sngl.Parse("t.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, diags := sngl.Check(doc, "."); len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("diagnostic: %s", d.Msg)
		}
	}
}
