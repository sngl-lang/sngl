//go:build !js

package gtk4_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestComponentFixtures(t *testing.T) {
	testutil.RunComponentFixtures(t, "gtk4")
}
