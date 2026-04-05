//go:build js

package html

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func (g *Generator) RunTests(*ast.Document, codegen.LangTranslator) ([]*codegen.TestResult, error) {
	return nil, nil
}
