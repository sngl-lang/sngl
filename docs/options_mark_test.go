package docs

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// The reference page documents the struct the #[options] mark names, not one
// named Options: the mark is what every options lookup keys on, so the docs
// have to read the same declaration the checker does.
//
// The keying itself is exercised where a decoy can be planted —
// internal/checker's options-mark test substitutes a package whose schema is
// called Knobs and whose Options is not one. Here the point is that the page
// is built from the checker's answer rather than from a second scan of the
// source.
func TestOptionsForPackageReadsTheMarkedStruct(t *testing.T) {
	sd := checker.OptionsStruct("std")
	if sd == nil {
		t.Fatal("sngl:std declares no #[options] struct")
	}
	opts := optionsForPackage("std")
	if len(opts) != len(sd.Fields) {
		t.Fatalf("optionsForPackage documented %d fields, the marked struct has %d", len(opts), len(sd.Fields))
	}
	for i, f := range sd.Fields {
		if opts[i].Name != f.Name {
			t.Errorf("field %d = %q, want %q", i, opts[i].Name, f.Name)
		}
	}
	var documented bool
	for _, o := range opts {
		documented = documented || o.Doc != ""
	}
	if !documented {
		t.Error("no field doc survived; the page reads no comments")
	}
}
