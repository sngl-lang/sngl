//go:build js

package fyne

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func (g *Generator) RunTests(*ir.Package, codegen.LangTranslator, *ir.StructLit) ([]*codegen.TestResult, error) {
	return nil, nil
}

func (g *Generator) ProbeTest() (bool, string) {
	return false, "fyne test runner unavailable in WASM build"
}
