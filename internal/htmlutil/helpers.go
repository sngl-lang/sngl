package htmlutil

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ExprToStaticValue extracts a static literal value from an expression.
// Returns "" if the expression is dynamic (not a literal).
func ExprToStaticValue(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.LiteralExpr:
		return LiteralRawToString(e)
	case *ast.UnitLiteral:
		return LiteralRawToString(&e.LiteralExpr)
	}
	return ""
}

// LiteralRawToString converts a v2 LiteralExpr to its display string.
func LiteralRawToString(lit *ast.LiteralExpr) string {
	switch lit.Kind {
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		// Strip quotes
		raw := lit.Raw
		if strings.HasPrefix(raw, `"""`) {
			return strings.TrimPrefix(strings.TrimSuffix(raw, `"""`), `"""`)
		}
		if len(raw) >= 2 {
			return raw[1 : len(raw)-1]
		}
		return raw
	case ast.LiteralBool, ast.LiteralInt, ast.LiteralFloat, ast.LiteralColor:
		return lit.Raw
	case ast.LiteralNull:
		return ""
	}
	return lit.Raw
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

// StaticStringArg extracts a static string value from an ArgList by name.
func StaticStringArg(args ast.ArgList, key string) string {
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == key {
			if lit, ok := arg.Value.(*ast.LiteralExpr); ok {
				return unquote(lit.Raw)
			}
		}
	}
	return ""
}

// StaticBoolArg extracts a static bool value from an ArgList by name.
func StaticBoolArg(args ast.ArgList, key string) bool {
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == key {
			if lit, ok := arg.Value.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralBool {
				return lit.Raw == "true"
			}
		}
	}
	return false
}

func unquote(raw string) string {
	if s, err := strconv.Unquote(raw); err == nil {
		return s
	}
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '`') {
		return raw[1 : len(raw)-1]
	}
	return raw
}

// IsSelfClosing reports whether an HTML tag is self-closing (void element).
func IsSelfClosing(tag string) bool {
	switch tag {
	case "input", "img", "br", "hr", "meta", "link", "area", "base", "col", "embed", "source", "track", "wbr":
		return true
	}
	return false
}
