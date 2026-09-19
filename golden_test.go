package sngl_test

import (
	"flag"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/goldentest"
)

var (
	update = flag.Bool("update", false, "rewrite the out/ golden and the run/ record in testdata/*.txtar")
	verify = flag.Bool("verify", false, "re-run every host toolchain, whatever the run/ record says")
)

// TestGolden compiles every testdata/*.txtar fixture for each target its
// source declares and compares the result against the archive's own `out/`.
//
// The fixtures sit in testdata/ beside the .sngl ones because they are the
// same kind of thing — a language feature and what it is meant to do — and the
// harness that runs them is internal/goldentest, whose doc comment carries the
// fixture format and the reasoning. Seed or refresh a golden with:
//
//	go test . -run TestGolden -update
func TestGolden(t *testing.T) {
	goldentest.Run(t, "testdata/*.txtar", *update, *verify)
}
