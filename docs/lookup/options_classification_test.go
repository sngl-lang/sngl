package lookup_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/docs/lookup"
)

// A target's #[options] struct is its option schema, not one of its types. The
// index says which by comparing the struct it found in the source against the
// one the checker read the mark off -- by pointer -- so this fails whenever the
// two sides reach the source through different parses.
//
// It is asserted through Lookup rather than through checker.OptionsStruct
// because the identity is what breaks, and only the real path exercises it:
// an earlier test that called PackageSource directly passed throughout, while
// `sngl doc html` classified Options as an ordinary type.
func TestATargetsOptionsStructIsClassifiedAsItsSchema(t *testing.T) {
	for _, name := range []string{"html", "go", "sngl:platform/html", "sngl:language/go", "sngl:app"} {
		t.Run(name, func(t *testing.T) {
			res, err := lookup.Lookup(name)
			if err != nil {
				t.Fatalf("Lookup(%q): %v", name, err)
			}
			if res.Index == nil {
				t.Fatalf("Lookup(%q) returned no index", name)
			}
			var inTypes, inPlatform bool
			for _, e := range res.Index.Types {
				if e.Name == "Options" {
					inTypes = true
				}
			}
			for _, d := range res.Index.PlatformTypes {
				if d.Name == "Options" {
					inPlatform = true
				}
			}
			if !inPlatform {
				t.Errorf("Options is not classified as %s's option schema (listed as an ordinary type: %v)", name, inTypes)
			}
			if inTypes {
				t.Errorf("Options is also listed as an ordinary type of %s", name)
			}
		})
	}
}
