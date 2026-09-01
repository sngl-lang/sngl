package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

// CompParams extracts Param entries from a ComponentDecl's Props.
func CompParams(comp *ast.ComponentDecl) []ast.Param {
	var out []ast.Param
	for _, p := range comp.Props.Props {
		if param, ok := p.(ast.Param); ok {
			out = append(out, param)
		}
	}
	return out
}
