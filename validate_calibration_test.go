package sngl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestValidateNoFalsePositives calibrates ir.Validate: every testdata fixture
// that checks cleanly (no error diagnostics, no ERROR directive) must produce
// zero Validate violations on its post-check IR. This guards against the
// validator asserting invariants that valid IR does not actually satisfy.
func TestValidateNoFalsePositives(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		f := f
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Skip fixtures that intentionally fail parse/check.
		if strings.Contains(string(src), "ERROR(") {
			continue
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			doc, err := sngl.Parse(f, strings.NewReader(string(src)))
			if err != nil {
				t.Skipf("parse error: %v", err)
			}
			pkg, diags := sngl.Check(doc, "testdata")
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Skipf("check error: %v", d.Msg)
				}
			}
			if pkg == nil {
				t.Skip("nil package")
			}
			for _, v := range ir.Validate(pkg) {
				t.Errorf("%s: %v", filepath.Base(f), v)
			}
		})
	}
}
