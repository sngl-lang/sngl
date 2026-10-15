//go:build !js

package html_test

import (
	"flag"
	"testing"

	"duckfam.us/sngl/internal/testutil"

	// Every target and scheme, as cmd/sngl registers them, so a fixture is
	// checked as `sngl test` checks it.
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	_ "duckfam.us/sngl/codegen/scheme"
)

var update = flag.Bool("update", false, "rerun every component fixture in Chromium and rewrite its testrun/ records")

func TestComponentFixtures(t *testing.T) {
	testutil.RunComponentFixtures(t, "html", *update)
}
