//go:build js

package html

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func (g *Generator) RunTests(*ir.Package, codegen.LangTranslator) ([]*codegen.TestResult, error) {
	return nil, nil
}

func (g *Generator) ProbeTest() (bool, string) {
	return false, "browser test runner unavailable in WASM build"
}
