package sngl_test

import (
	"flag"
	"os"
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/goldentest"
	"duckfam.us/sngl/internal/headless"
	"duckfam.us/sngl/internal/testutil"
)

var (
	update = flag.Bool("update", false, "rewrite the out/ golden and the run/ record in testdata/*.txtar")
	verify = flag.Bool("verify", false, "re-run every host toolchain, whatever the run/ record says")
)

// TestMain re-execs inside the headless compositor when it may verify, where a
// golden's exec directives run programs that present windows. It runs before
// flag.Parse, so it reads the flags off the command line.
func TestMain(m *testing.M) {
	headless.RunShim()
	if slices.ContainsFunc(os.Args[1:], verifies) {
		testutil.MaybeReexecUnderCage()
	}
	os.Exit(m.Run())
}

func verifies(arg string) bool {
	name, value, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	return (name == "update" || name == "verify") && value != "false" && value != "0"
}

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
