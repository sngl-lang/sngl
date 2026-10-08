package main

import (
	"os"
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

// TestMain re-execs the CLI tests inside a headless cage compositor so the
// gtk4 snapshot script test (which presents a real GtkWindow via
// `sngl test --platform=gtk4`) doesn't flash on the developer's desktop.
// No-op under `go tool verify` (already caged) or when cage is unavailable.
func TestMain(m *testing.M) {
	testutil.MaybeReexecUnderCage()
	os.Exit(m.Run())
}
