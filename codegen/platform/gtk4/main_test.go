package gtk4_test

import (
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// TestMain re-execs this package's tests inside a headless cage compositor so
// the gtk4 snapshot tests (which present real GtkWindows) don't flash on the
// developer's desktop. No-op under `go tool verify` (already caged) or when
// cage is unavailable — in the latter case the GUI tests skip themselves.
func TestMain(m *testing.M) {
	testutil.MaybeReexecUnderCage()
	os.Exit(m.Run())
}
