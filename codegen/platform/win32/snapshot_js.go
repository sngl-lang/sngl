//go:build js

package win32

import (
	"fmt"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func (g *Generator) Snapshot(*ir.Package, codegen.LangTranslator, int, int) ([]byte, error) {
	return nil, fmt.Errorf("win32 snapshot unavailable in WASM build")
}
