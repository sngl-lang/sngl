package testutil

import "testing"

// The skip must fire on a platform's unimplemented-component diagnostic and
// on nothing else: a build failure or a missing host tool that matched here
// would be reported as an unsupported feature and stop failing the suite.
func TestUnsupportedComponentReason(t *testing.T) {
	skips := map[string]string{
		`main.sngl: component "progress" has no gtk4 implementation`: "gtk4 does not implement progress",
		`component "table" has no android implementation`:            "android does not implement table",
	}
	for out, want := range skips {
		got, ok := unsupportedComponentReason(out)
		if !ok {
			t.Errorf("no skip for %q", out)
			continue
		}
		if got != want {
			t.Errorf("reason for %q = %q, want %q", out, got, want)
		}
	}

	fails := []string{
		"# command-line-arguments\n./model.go:14:2: undefined: gtk_foo",
		"Package gtk4 was not found in the pkg-config search path",
		`irwalk.EvalStmt: unhandled ir.Stmt *ir.ErrorBoundary`,
		`component "progress" has no implementation`, // no platform named
		"MainScreenTest > testSiblingComputedCall FAILED",
	}
	for _, out := range fails {
		if reason, ok := unsupportedComponentReason(out); ok {
			t.Errorf("%q skipped as %q; it is a real failure", out, reason)
		}
	}
}
