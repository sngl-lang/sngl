//go:build !js

package html_test

import (
	"testing"

	"duckfam.us/sngl/internal/testutil"
)

func TestComponentFixtures(t *testing.T) {
	testutil.RunComponentFixtures(t, "html")
}
