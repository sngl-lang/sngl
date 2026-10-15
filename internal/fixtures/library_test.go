package fixtures

import (
	"testing"

	"duckfam.us/sngl/internal/checker"
	_ "duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/lib"
)

// TestLibraryHoldsNoInvalidType holds every library package to what a clean
// fixture is held to (assertNoSilentInvalid): a TypeInvalid in library source
// is a call or a declaration the checker gave up on without saying so, and
// one that IsAssignableTo then lets any misuse of through.
func TestLibraryHoldsNoInvalidType(t *testing.T) {
	for _, name := range lib.Packages() {
		pkg, _ := checker.CheckLibPackage(name)
		if pkg == nil {
			continue
		}
		for _, at := range InvalidTypes(pkg) {
			t.Errorf("sngl:%s: TypeInvalid at %s (%d:%d)", name, at.Where, at.Pos.Line, at.Pos.Column)
		}
	}
}
