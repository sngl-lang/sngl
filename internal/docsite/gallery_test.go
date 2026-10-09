package docsite_test

import (
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/docsite"
)

// A hand-picked list rots unless something holds it to the library. The tier
// table this replaced named nine components that had been renamed or never
// existed, and every one of them silently rendered nothing.
func TestStarterComponentsExist(t *testing.T) {
	schema := checker.PackageSchema(docsite.StarterPkg)
	if len(schema) == 0 {
		t.Fatalf("sngl:%s declares no components", docsite.StarterPkg)
	}
	for _, name := range docsite.Starter {
		if _, ok := schema[name]; !ok {
			t.Errorf("starter list names %q, which sngl:%s does not declare", name, docsite.StarterPkg)
		}
	}
	seen := map[string]bool{}
	for _, name := range docsite.Starter {
		if seen[name] {
			t.Errorf("starter list names %q twice", name)
		}
		seen[name] = true
	}
}
