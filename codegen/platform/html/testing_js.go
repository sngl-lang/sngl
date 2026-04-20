//go:build js

package html

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func (g *Generator) RunTests(*ir.Package, codegen.LangTranslator) ([]*codegen.TestResult, error) {
	return nil, nil
}
