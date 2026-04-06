package htmlutil

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ExprToStaticValue extracts a static literal value from an expression.
// Returns "" if the expression is dynamic (not a literal).
func ExprToStaticValue(expr ast.Expr) string {
	if expr.Literal != nil {
		return LiteralToString(expr.Literal)
	}
	if lit, ok := expr.SNGL.(*ast.LiteralExpr); ok {
		return LiteralToString(lit.Value)
	}
	return ""
}

// LiteralToString converts a literal value to its string representation.
func LiteralToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%v", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	}
	return ""
}

// StaticString extracts a static string value from a prop map.
func StaticString(props map[string]ast.Expr, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}
	if s, ok := v.Literal.(string); ok {
		return s
	}
	if v.SNGL != nil {
		if lit, ok := v.SNGL.(*ast.LiteralExpr); ok {
			if s, ok := lit.Value.(string); ok {
				return s
			}
			return fmt.Sprint(lit.Value)
		}
	}
	return ""
}

// StaticBool extracts a static bool value from a prop map.
func StaticBool(props map[string]ast.Expr, key string) bool {
	v, ok := props[key]
	if !ok {
		return false
	}
	if b, ok := v.Literal.(bool); ok {
		return b
	}
	return false
}

// IsSelfClosing reports whether an HTML tag is self-closing (void element).
func IsSelfClosing(tag string) bool {
	switch tag {
	case "input", "img", "br", "hr", "meta", "link", "area", "base", "col", "embed", "source", "track", "wbr":
		return true
	}
	return false
}
