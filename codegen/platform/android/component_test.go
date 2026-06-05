//go:build !js

package android_test

import (
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestComponentFixtures(t *testing.T) {
	// Android component fixtures build native artifacts (AGP/Robolectric),
	// which is slow and can hang/timeout in sandboxes. Opt in with
	// SNGL_TESTS_FULL=1 (set by `go tool verify -full`).
	if os.Getenv("SNGL_TESTS_FULL") == "" {
		t.Skip("set SNGL_TESTS_FULL=1 (or run `go tool verify -full`) to run android component fixtures")
	}
	testutil.RunComponentFixtures(t, "android")
}
