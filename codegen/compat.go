package codegen

import (
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Extraction helpers for v2 Document. The v2 Document only has Stmts []Stmt;
// these helpers provide the v1-style named-slice access patterns that
// downstream code (platforms, cmd) still uses during the migration.

// --- Expr helpers ---

// ExprLiteralString extracts a string value from a LiteralExpr.
func ExprLiteralString(e ast.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return "", false
	}
	switch lit.Kind {
	case ast.LiteralStringQuoted:
		if s, err := strconv.Unquote(lit.Raw); err == nil {
			return s, true
		}
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1], true
		}
		return lit.Raw, true
	case ast.LiteralStringBackticked:
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '`' && raw[len(raw)-1] == '`' {
			return raw[1 : len(raw)-1], true
		}
		return lit.Raw, true
	case ast.LiteralStringTrippleQuoted:
		raw := lit.Raw
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) {
			return raw[3 : len(raw)-3], true
		}
		return lit.Raw, true
	}
	return "", false
}

// --- ComponentDecl helpers ---

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
