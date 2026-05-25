//go:build !js

package android_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestComponentFixtures(t *testing.T) {
	testutil.RunComponentFixtures(t, "android")
}
