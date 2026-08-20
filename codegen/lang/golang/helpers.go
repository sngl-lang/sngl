package golang

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ir"
)

// translateIRLiteral renders an ir.Literal as its Go source form. Used by
// Translator.TranslateIRLiteral (the LangTranslator literal hook); the main
// expression path uses GoIRContext.evalLiteral. Unit/temporal literals route
// through the dedicated Lower*LiteralGo helpers.
func translateIRLiteral(n *ir.Literal) string {
	if n == nil {
		return "nil"
	}
	if n.Suffix != "" {
		if out, ok := LowerUnitLiteralGo(n); ok {
			return out
		}
		// Non-unit literal that carries a suffix (shouldn't normally
		// happen) — fall back to a quoted "raw+suffix" string.
		return fmt.Sprintf("%q", n.Raw)
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeString, ir.TypeColor:
			return fmt.Sprintf("%q", n.Raw)
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Raw
		case ir.TypeNull:
			return "nil"
		case ir.TypeStruct:
			if out, ok := LowerTimeLiteralGo(n); ok {
				return out
			}
			if ir.StringReprStruct(n.Type) {
				return fmt.Sprintf("%q", n.Raw)
			}
		}
	}
	return n.Raw
}

// ExportName capitalizes the first letter for Go exported names.
func ExportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
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
	case "date", "time", "datetime":
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

// ComponentRenderMethod returns the Model method name a Go backend emits for
// a user component's per-instance render (e.g. "renderTreeView" for "TreeView").
// Shared by every Go platform (fyne, bubbletea, gtk4) and the
// lower.CreateComponent dispatch so call sites and definitions stay in sync.
func ComponentRenderMethod(componentName string) string {
	return "render" + ExportName(componentName)
}

// ZeroValueGo returns the Go zero-value expression for a SNGL type hint or
// a Go type string (func(...), []T, pkg.T, etc.).
func ZeroValueGo(hint string) string {
	// Fast-path on Go syntax — don't re-run TypeHintToGo which would mangle
	// `func(...)` into `Func(...)`.
	if strings.HasPrefix(hint, "func(") || strings.HasPrefix(hint, "func ") {
		return "nil"
	}
	if strings.HasPrefix(hint, "[]") || strings.HasPrefix(hint, "*") {
		return "nil"
	}
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
	case "any":
		return "nil"
	default:
		if strings.HasPrefix(goType, "[]") {
			return "nil"
		}
		if strings.HasPrefix(goType, "*") {
			return "nil"
		}
		if strings.HasPrefix(goType, "func(") || strings.HasPrefix(goType, "func ") {
			return "nil"
		}
		// Named/qualified type → struct literal zero.
		return goType + "{}"
	}
}
