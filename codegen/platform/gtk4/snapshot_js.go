//go:build js

package gtk4

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func (g *Generator) Snapshot(*ir.Package, codegen.LangTranslator, int, int) ([]byte, error) {
	return nil, fmt.Errorf("gtk4 snapshot unavailable in WASM build")
}

func (g *Generator) BatchSnapshot([]codegen.BatchDoc, int, int) (map[string][]byte, error) {
	return nil, fmt.Errorf("gtk4 batch snapshot unavailable in WASM build")
}
