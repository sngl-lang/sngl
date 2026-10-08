package lower_test

import (
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"

	_ "duckfam.us/sngl/internal/testtargets"
)

// TestGenVocabularyIsMapped holds the SNGL enums and the Go tables together.
// A member nothing maps is a word a target can write that gates no pass, which
// is the failure the mark table exists to prevent one layer up.
//
// `Rendered` is the one enum not folded into Features: it describes a
// primitive rather than a target, so it is read off the declaration by
// ir.HoldsIdentity and never becomes a field. Its members are held to that
// instead -- a word here still has to mean something to somebody.
func TestGenVocabularyIsMapped(t *testing.T) {
	pkg := checker.LibPackage("x/gen")
	if pkg == nil {
		t.Fatal("no package sngl:x/gen")
	}
	seen := map[string]bool{}
	for _, ed := range pkg.Enums {
		for _, m := range ed.Members {
			seen[ed.Name] = true
			if ed.Name == "Rendered" {
				if m.Name != ir.RenderedIdentity && m.Name != ir.RenderedSurface {
					t.Errorf("sngl:x/gen declares Rendered.%s, which ir reads nowhere", m.Name)
				}
				continue
			}
			if !lower.GenNameIsMapped(ed.Name, m.Name) {
				t.Errorf("sngl:x/gen declares %s.%s, which no Features field holds", ed.Name, m.Name)
			}
		}
	}
	for _, want := range []string{"Capability", "Pass", "Rendered"} {
		if !seen[want] {
			t.Errorf("sngl:x/gen declares no enum %s; the marks have no vocabulary", want)
		}
	}
}
