//go:build !js

package html_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestComponentFixtures(t *testing.T) {
	testutil.RunComponentFixtures(t, "html")
}
