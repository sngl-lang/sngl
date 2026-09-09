package lookup_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/docs/lookup"
)

// A target's build-directive node is the declaration an output block writes
// and the one its build options are declared on, so `sngl doc <target>` has to
// list it. It is an ordinary component of the target's package, which is the
// point: the option schema is documented by the same code that documents every
// other component, rather than by a section of its own.
func TestATargetsBuildNodeIsDocumented(t *testing.T) {
	for _, tc := range []struct{ path, node string }{
		{"html", "html"},
		{"go", "go"},
		{"sngl:platform/html", "html"},
		{"sngl:language/go", "go"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res, err := lookup.Lookup(tc.path)
			if err != nil {
				t.Fatalf("Lookup(%q): %v", tc.path, err)
			}
			if res.Index == nil {
				t.Fatalf("Lookup(%q) returned no index", tc.path)
			}
			for _, c := range res.Index.Components {
				if c.Name == tc.node {
					return
				}
			}
			t.Errorf("%s does not document its build node %q", tc.path, tc.node)
		})
	}
}
