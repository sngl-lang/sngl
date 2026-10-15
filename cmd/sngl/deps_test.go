package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTheCompilerDoesNotLinkAToolkit is the invariant every platform in this
// repo is written around.
//
// gtk4 keeps its cgo in string literals and templates; fyne is named only in
// string literals, with keep_test_deps.go there to stop `go mod tidy`
// noticing. The result is that `sngl` is a pure-Go binary: it cross-compiles,
// builds on a host with no GL or GTK headers, and the WASM playground can
// import the compiler at all.
//
// pkg/go/fynehost links Fyne for real, because a worker must. Nothing the
// compiler reaches may import it, and this is what says so.
func TestTheCompilerDoesNotLinkAToolkit(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	banned := []string{
		"fyne.io/",
		"duckfam.us/sngl/pkg/go/fynehost",
		"github.com/fogleman/gg",
	}
	var deps int
	for dep := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		deps++
		for _, b := range banned {
			if strings.HasPrefix(dep, b) {
				t.Errorf("sngl links %s; the compiler is no longer pure Go", dep)
			}
		}
	}
	if deps < 50 {
		t.Fatalf("go list reported %d dependencies; this test is checking nothing", deps)
	}
}
