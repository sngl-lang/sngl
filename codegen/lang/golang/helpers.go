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
	if expr.TypeHint != "" {
		return TypeHintToGo(expr.TypeHint)
	}
	if expr.Literal != nil {
		switch expr.Literal.(type) {
		case int:
			return "int"
		case float64:
			return "float64"
		case bool:
			return "bool"
		case string:
			return "string"
		}
	}
	return "any"
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

// LiteralToGo converts an ast.Expr literal to a Go literal string.
func LiteralToGo(expr ast.Expr) string {
	if strings.HasPrefix(expr.TypeHint, "option:") {
		if expr.Literal == nil && expr.SNGL == nil {
			return "nil"
		}
		inner := expr
		inner.TypeHint = expr.TypeHint[7:]
		val := LiteralToGo(inner)
		if val == "nil" {
			return "nil"
		}
		goType := TypeHintToGo(inner.TypeHint)
		return fmt.Sprintf("func() *%s { v := %s; return &v }()", goType, val)
	}
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			switch expr.TypeHint {
			case "duration":
				return fmt.Sprintf("mustParseDuration(%q)", v)
			case "date":
				return fmt.Sprintf("mustParseDate(%q)", v)
			case "time":
				return fmt.Sprintf("mustParseTime(%q)", v)
			case "dateTime":
				return fmt.Sprintf("mustParseDateTime(%q)", v)
			default:
				return fmt.Sprintf("%q", v)
			}
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	// Handle SNGL expression nodes for compound initializers (lists, structs).
	if expr.SNGL != nil {
		switch n := expr.SNGL.(type) {
		case *ast.ListExpr:
			if len(n.Elements) == 0 {
				return "nil"
			}
			parts := make([]string, len(n.Elements))
			for i, el := range n.Elements {
				parts[i] = LiteralToGo(ast.Expr{SNGL: el})
			}
			elemType := ""
			if strings.HasPrefix(expr.TypeHint, "list<") {
				elemType = ExportName(strings.TrimSuffix(strings.TrimPrefix(expr.TypeHint, "list<"), ">"))
			} else if se, ok := n.Elements[0].(*ast.StructExpr); ok && se.Name != "" {
				elemType = ExportName(se.Name)
			} else if ce, ok := n.Elements[0].(*ast.CallExpr); ok {
				elemType = ExportName(ce.Func)
			} else if lit, ok := n.Elements[0].(*ast.LiteralExpr); ok {
				switch lit.Kind {
				case ast.LiteralInt:
					elemType = "int"
				case ast.LiteralFloat:
					elemType = "float64"
				case ast.LiteralString:
					elemType = "string"
				case ast.LiteralBool:
					elemType = "bool"
				}
			}
			return "[]" + elemType + "{" + strings.Join(parts, ", ") + "}"
		case *ast.CallExpr:
			// Struct constructor: Todo("text", false) → Todo{Text: "text", Done: false}
			parts := make([]string, len(n.Args))
			for i, a := range n.Args {
				parts[i] = LiteralToGo(ast.Expr{SNGL: a})
			}
			return ExportName(n.Func) + "{" + strings.Join(parts, ", ") + "}"
		case *ast.StructExpr:
			parts := make([]string, len(n.Fields))
			for i, f := range n.Fields {
				parts[i] = ExportName(f.Name) + ": " + LiteralToGo(ast.Expr{SNGL: f.Value})
			}
			return ExportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
		case *ast.LiteralExpr:
			return LiteralToGo(ast.Expr{Literal: n.Value, TypeHint: expr.TypeHint})
		}
	}
	if expr.TypeHint != "" {
		if expr.Resolved != nil && expr.Resolved.NativeType != "" {
			return expr.Resolved.NativeType + "{}"
		}
		return "nil"
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
func SnglNodeGoType(e ast.Node) string {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		switch n.Kind {
		case ast.LiteralInt:
			return "int"
		case ast.LiteralFloat:
			return "float64"
		case ast.LiteralBool:
			return "bool"
		case ast.LiteralString:
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
		switch n.Func {
		case "string":
			return "string"
		case "int":
			return "int"
		case "float":
			return "float64"
		case "size":
			return "int"
		}
	case *ast.InterpolationExpr:
		return "string"
	case *ast.MethodExpr:
		switch n.Method {
		case "upper", "lower", "trim", "replace", "substring":
			return "string"
		case "length", "indexOf":
			return "int"
		}
	case *ast.ParenExpr:
		return SnglNodeGoType(n.Inner)
	case *ast.IdentExpr:
		if n.ResolvedType != "" {
			return TypeHintToGo(n.ResolvedType)
		}
	case *ast.SelectExpr:
		if n.ResolvedType != "" {
			return TypeHintToGo(n.ResolvedType)
		}
	case *ast.IndexExpr:
		if n.ResolvedType != "" {
			return TypeHintToGo(n.ResolvedType)
		}
	}
	return "any"
}
