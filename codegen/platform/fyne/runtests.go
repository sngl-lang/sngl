//go:build !js

package fyne

import (
	"os/exec"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// RunTests is the entry point for `sngl test --platform=fyne`. It groups
// the package's test functions by their target component, generates a
// temp Go module per component, shells `go test -json`, and parses
// results back. The pipeline is fleshed out in subsequent tasks; for
// now the skeleton returns nil so the matrix runner can call us without
// erroring.
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	return nil, nil
}

// ProbeTest reports whether a Go toolchain is available. We don't probe
// for fyne build deps directly — that would require running a real
// `go build`, which is too expensive for a cheap probe. Actual fyne
// dependencies surface at RunTests time as a build failure reported
// through the test results.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	return true, ""
}
