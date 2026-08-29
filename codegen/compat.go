package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

// ExprLiteralString extracts the value of a string literal expression.
func ExprLiteralString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok || lit == nil {
		return "", false
	}
	return lit.StringValue()
}

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
