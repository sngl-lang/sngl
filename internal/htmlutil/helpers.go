package htmlutil

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ExprToStaticValueIR extracts a static literal value from an IR expression,
// returning "" for non-literals. Mirrors ExprToStaticValue but consumes ir.Expr.
func ExprToStaticValueIR(e ir.Expr) string {
	if sl, ok := e.(*ir.StructLit); ok {
		if css, ok := colorStructToCSS(sl); ok {
			return css
		}
		return ""
	}
	lit, ok := e.(*ir.Literal)
	if !ok || lit == nil {
		return ""
	}
	return irLiteralStaticValue(lit)
}

// colorStructToCSS renders a `color{r,g,b,a}` struct literal — the lowered
// form of a `#rrggbb[aa]` literal (see checker.lowerHexLiteral) — to a CSS
// color string: `#rrggbb` when fully opaque, otherwise `rgba(r,g,b,a)`.
// Detection is structural (fields r,g,b[,a] of static ints) because constant
// folding drops the StructLit's Type/Def, so ir.IsColorStruct can't be relied
// on by codegen. Returns ok=false when the literal isn't a color shape.
func colorStructToCSS(sl *ir.StructLit) (string, bool) {
	if sl.Def == nil && sl.Type == nil {
		// Untyped struct: only treat as color when shaped exactly like one.
		if len(sl.Fields) < 3 || len(sl.Fields) > 4 {
			return "", false
		}
	} else if !ir.IsColorStruct(sl.Type) && !(sl.Def != nil && sl.Def.Name == "color") {
		return "", false
	}
	ch := map[string]int{"a": 255}
	for _, f := range sl.Fields {
		switch f.Name {
		case "r", "g", "b", "a":
		default:
			return "", false
		}
		lit, ok := f.Value.(*ir.Literal)
		if !ok || lit == nil {
			return "", false
		}
		n, err := strconv.Atoi(strings.TrimSpace(lit.Raw))
		if err != nil {
			return "", false
		}
		ch[f.Name] = n
	}
	r, rok := ch["r"]
	g, gok := ch["g"]
	b, bok := ch["b"]
	if !rok || !gok || !bok {
		return "", false
	}
	if a := ch["a"]; a < 255 {
		return fmt.Sprintf("rgba(%d,%d,%d,%g)", r, g, b, float64(a)/255), true
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b), true
}

func irLiteralStaticValue(lit *ir.Literal) string {
	if lit == nil {
		return ""
	}
	// Unit literals carry their suffix separately (e.g. "16"+"px").
	if lit.Suffix != "" {
		return lit.Raw
	}
	if lit.Type == nil {
		return lit.Raw
	}
	switch lit.Type.Kind {
	case ir.TypeString:
		raw := lit.Raw
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) && len(raw) >= 6 {
			return raw[3 : len(raw)-3]
		}
		if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '`') && raw[len(raw)-1] == raw[0] {
			return raw[1 : len(raw)-1]
		}
		return raw
	case ir.TypeNull:
		return ""
	}
	return lit.Raw
}

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
