//go:build !js

package gtk4rt

import (
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// TestMain re-execs under a headless cage compositor so TestSnapshotRender
// (which presents a GtkWindow) doesn't flash on the developer's desktop.
func TestMain(m *testing.M) {
	testutil.MaybeReexecUnderCage()
	os.Exit(m.Run())
}
