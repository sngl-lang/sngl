package golang

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ExportName capitalizes the first letter for Go exported names.
func ExportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// astLiteralToGo renders an ast.LiteralExpr as its Go source form (quoted
// strings, raw numeric literals). Shared by LiteralToGo; the IR code path
// uses translateIRLiteral on ir.Literal.
func astLiteralToGo(n *ast.LiteralExpr) string {
	switch n.Kind {
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted,
		ast.LiteralColor, ast.LiteralUnit:
		return fmt.Sprintf("%q", n.Raw)
	case ast.LiteralNull:
		return "nil"
	}
	return n.Raw
}

// UnexportName lowercases the first letter for Go unexported names.
func UnexportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// InferGoType infers a Go type string from an ast.Expr.
func InferGoType(expr ast.Expr) string {
	if expr == nil {
		return "any"
	}
	return SnglNodeGoType(expr)
}

// TypeHintToGo converts a SNGL type hint to a Go type string.
func TypeHintToGo(hint string) string {
	if strings.HasPrefix(hint, "[]") {
		return "[]" + TypeHintToGo(hint[2:])
	}
	if strings.HasPrefix(hint, "list:") {
		return "[]" + TypeHintToGo(hint[5:])
	}
	if strings.HasPrefix(hint, "option:") {
		return "*" + TypeHintToGo(hint[7:])
	}
	if strings.HasPrefix(hint, "enum:") {
		return "string"
	}
	switch hint {
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "string"
	case "date", "time", "dateTime":
		return "time.Time"
	case "duration":
		return "time.Duration"
	default:
		if strings.Contains(hint, ".") && !strings.ContainsAny(hint, ":~<>") {
			return hint
		}
		if !strings.ContainsAny(hint, ":~<>") && hint != "" {
			return ExportName(hint)
		}
		return "any"
	}
}

// ZeroValueGo returns the Go zero-value expression for a SNGL type hint.
func ZeroValueGo(hint string) string {
	goType := TypeHintToGo(hint)
	switch goType {
	case "int":
		return "0"
	case "float64":
		return "0.0"
	case "bool":
		return "false"
	case "string":
		return `""`
	case "time.Time":
		return "time.Time{}"
	case "time.Duration":
		return "0"
	default:
		if strings.HasPrefix(goType, "[]") {
			return "nil"
		}
		if strings.HasPrefix(goType, "*") {
			return "nil"
		}
		return `""`
	}
}

// ExternFuncGoType returns the Go function type for an extern function declaration.
func ExternFuncGoType(paramTypes []string, returnType string) string {
	params := make([]string, len(paramTypes))
	for i, p := range paramTypes {
		params[i] = TypeHintToGo(p)
	}
	sig := "func(" + strings.Join(params, ", ") + ")"
	if returnType != "" {
		sig += " " + TypeHintToGo(returnType)
	}
	return sig
}

// LiteralToGo converts an ast.Expr to a Go literal string.
func LiteralToGo(expr ast.Expr) string {
	if expr == nil {
		return `""`
	}
	switch n := expr.(type) {
	case *ast.LiteralExpr:
		return astLiteralToGo(n)
	case *ast.UnitLiteral:
		return fmt.Sprintf("%q", n.Raw+n.Suffix)
	case *ast.ListExpr:
		if len(n.Elements) == 0 {
			return "nil"
		}
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = LiteralToGo(el)
		}
		elemType := ""
		if se, ok := n.Elements[0].(*ast.StructExpr); ok && se.Name != "" {
			elemType = ExportName(se.Name)
		} else if ce, ok := n.Elements[0].(*ast.CallExpr); ok {
			if ident, ok2 := ce.Func.(*ast.IdentExpr); ok2 {
				elemType = ExportName(ident.Name)
			}
		} else if lit, ok := n.Elements[0].(*ast.LiteralExpr); ok {
			switch lit.Kind {
			case ast.LiteralInt:
				elemType = "int"
			case ast.LiteralFloat:
				elemType = "float64"
			case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
				elemType = "string"
			case ast.LiteralBool:
				elemType = "bool"
			}
		}
		return "[]" + elemType + "{" + strings.Join(parts, ", ") + "}"
	case *ast.CallExpr:
		// Struct constructor: Todo("text", false) → Todo{Text: "text", Done: false}
		var args []ast.Expr
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				args = append(args, arg.Value)
			}
		}
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = LiteralToGo(a)
		}
		fn := ""
		if ident, ok := n.Func.(*ast.IdentExpr); ok {
			fn = ident.Name
		}
		return ExportName(fn) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.StructExpr:
		parts := make([]string, len(n.Fields))
		for i, f := range n.Fields {
			parts[i] = ExportName(f.Name) + ": " + LiteralToGo(f.Value)
		}
		return ExportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
	}
	return `""`
}

// NeedsTimeType returns true if the type hint requires the time package.
func NeedsTimeType(hint string) bool {
	switch hint {
	case "date", "time", "dateTime", "duration":
		return true
	}
	return false
}

// SnglNodeGoType infers a Go type from a SNGL expression node.
func SnglNodeGoType(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		switch n.Kind {
		case ast.LiteralInt:
			return "int"
		case ast.LiteralFloat:
			return "float64"
		case ast.LiteralBool:
			return "bool"
		case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
			return "string"
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte, ast.BinAnd, ast.BinOr:
			return "bool"
		case ast.BinDiv:
			return "float64"
		default:
			lt := SnglNodeGoType(n.Left)
			rt := SnglNodeGoType(n.Right)
			if lt == "float64" || rt == "float64" {
				return "float64"
			}
			return lt
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNot {
			return "bool"
		}
		return SnglNodeGoType(n.Operand)
	case *ast.TernaryExpr:
		return SnglNodeGoType(n.Then)
	case *ast.CallExpr:
		if ident, ok := n.Func.(*ast.IdentExpr); ok {
			switch ident.Name {
			case "string":
				return "string"
			case "int":
				return "int"
			case "float":
				return "float64"
			case "size":
				return "int"
			}
		}
		// Method call: check the method name for known return types
		if sel, ok := n.Func.(*ast.SelectExpr); ok {
			switch sel.Field {
			case "upper", "lower", "trim", "replace", "substring":
				return "string"
			case "length", "indexOf":
				return "int"
			}
		}
	case *ast.InterpolationExpr:
		return "string"
	case *ast.ParenExpr:
		return SnglNodeGoType(n.Inner)
	case *ast.SelectExpr:
		// TODO: resolve from IR once codegen migrates
	case *ast.IndexExpr:
		// TODO: resolve from IR once codegen migrates
	}
	return "any"
}
