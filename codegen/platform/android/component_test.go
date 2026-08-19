//go:build !js

package android_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestComponentFixtures(t *testing.T) {
	// Android component fixtures build native artifacts (AGP/Robolectric),
	// which is slow. They run by default; use `go test -short` to skip them
	// (e.g. for a fast local loop). The standard Go convention replaces the
	// former SNGL_TESTS_FULL opt-in.
	if testing.Short() {
		t.Skip("skipping slow android component fixtures in -short mode")
	}
	testutil.RunComponentFixtures(t, "android")
}
