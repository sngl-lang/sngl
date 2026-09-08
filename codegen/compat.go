package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

// CompParams extracts the prop entries from a ComponentDecl's Props. A slot is
// a Param whose type is a component type; it declares no value, so it is not
// one of these.
func CompParams(comp *ast.ComponentDecl) []ast.Param {
	var out []ast.Param
	for _, p := range comp.Props.Props {
		if param, ok := p.(ast.Param); ok && !param.IsSlot() {
			out = append(out, param)
		}
	}
	return out
}
