//go:build !js

package gtk4rt

import (
	"os"
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

// TestMain re-execs under a headless cage compositor so TestSnapshotRender
// (which presents a GtkWindow) doesn't flash on the developer's desktop.
func TestMain(m *testing.M) {
	testutil.MaybeReexecUnderCage()
	// cage's headless backend has no GPU: once a second window was shown,
	// GSK's GL renderer failed to make its context current and epoxy aborted.
	if os.Getenv("GSK_RENDERER") == "" {
		os.Setenv("GSK_RENDERER", "cairo")
	}
	os.Exit(m.Run())
}
