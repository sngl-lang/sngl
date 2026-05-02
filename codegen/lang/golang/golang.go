package golang

import (
	_ "embed"
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed golang.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	if doc, _ := parser.Parse("golang.sngl", []byte(pkgSource)); doc != nil {
		pkgDocs = []*ast.Document{doc}
	}
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for Go.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string { return "go" }
func (t *Translator) Description() string {
	return "Generate Go source. Supports HTTP route mode and WASM bindings."
}
func (t *Translator) Package() []*ast.Document            { return pkgDocs }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }
func (t *Translator) Capabilities() lower.Caps            { return lower.Caps{} }

// v2 IR-based methods (stubs — will be implemented during platform migration).

func (t *Translator) WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error {
	return fmt.Errorf("golang: WriteExpr not yet implemented")
}

func (t *Translator) WriteStmt(w io.Writer, stmt ir.Stmt, scope *ir.Scope) error {
	return fmt.Errorf("golang: WriteStmt not yet implemented")
}

func (t *Translator) WriteType(w io.Writer, typ *ir.Type) error {
	return fmt.Errorf("golang: WriteType not yet implemented")
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return ExportName(name.Name)
}

func (t *Translator) Eval(expr ir.Expr) string {
	return fmt.Sprintf("/* eval not implemented: %T */", expr)
}

func (t *Translator) TranslateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	return translateIRExpr(e, scope)
}

func (t *Translator) TranslateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	return translateIRMutation(s, scope)
}

func (t *Translator) TranslateIRLiteral(e ir.Expr) string {
	if lit, ok := e.(*ir.Literal); ok {
		return translateIRLiteral(lit)
	}
	return `""`
}

func (t *Translator) TypeToNative(hint string) string {
	if strings.HasPrefix(hint, "option:") {
		return "*" + t.TypeToNative(hint[7:])
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
	default:
		return "any"
	}
}

func (t *Translator) ExportName(name string) string {
	return ExportName(name)
}

func binaryOpStr(op ast.BinaryOp) string {
	switch op {
	case ast.BinAdd:
		return "+"
	case ast.BinSub:
		return "-"
	case ast.BinMul:
		return "*"
	case ast.BinDiv:
		return "/"
	case ast.BinMod:
		return "%"
	case ast.BinEq:
		return "=="
	case ast.BinNeq:
		return "!="
	case ast.BinLt:
		return "<"
	case ast.BinLte:
		return "<="
	case ast.BinGt:
		return ">"
	case ast.BinGte:
		return ">="
	case ast.BinAnd:
		return "&&"
	case ast.BinOr:
		return "||"
	default:
		return "?"
	}
}

func assignOpStr(op ast.AssignOp) string {
	switch op {
	case ast.AssignSet:
		return "="
	case ast.AssignAdd:
		return "+="
	case ast.AssignSub:
		return "-="
	case ast.AssignMul:
		return "*="
	case ast.AssignDiv:
		return "/="
	case ast.AssignMod:
		return "%="
	default:
		return "="
	}
}

// goBuiltinMethodFromCall returns native Go code for stdlib methods, or "" if not a stdlib method.
// sel is the SelectExpr (receiver.method), args are the already-extracted argument expressions.
func goBuiltinMethodFromArgs(qualName string, argExprs []string) string {
	a := func(i int) string {
		if i < len(argExprs) {
			return argExprs[i]
		}
		return "nil"
	}

	switch qualName {
	case "int.min", "*.min":
		return "min(" + a(0) + ", " + a(1) + ")"
	case "int.max", "*.max":
		return "max(" + a(0) + ", " + a(1) + ")"
	case "int.abs", "*.abs":
		return "func(x int) int { if x < 0 { return -x }; return x }(" + a(0) + ")"
	case "int.clamp", "*.clamp":
		return "min(max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
	case "float.min":
		return "math.Min(" + a(0) + ", " + a(1) + ")"
	case "float.max":
		return "math.Max(" + a(0) + ", " + a(1) + ")"
	case "float.abs":
		return "math.Abs(" + a(0) + ")"
	case "float.clamp":
		return "math.Min(math.Max(" + a(0) + ", " + a(1) + "), " + a(2) + ")"
	case "float.floor", "*.floor":
		return "int(math.Floor(" + a(0) + "))"
	case "float.ceil", "*.ceil":
		return "int(math.Ceil(" + a(0) + "))"
	case "float.round", "*.round":
		return "int(math.Round(" + a(0) + "))"
	case "float.sqrt", "*.sqrt":
		return "math.Sqrt(" + a(0) + ")"
	case "float.pow", "*.pow":
		return "math.Pow(" + a(0) + ", " + a(1) + ")"
	case "float.sin", "*.sin":
		return "math.Sin(" + a(0) + ")"
	case "float.cos", "*.cos":
		return "math.Cos(" + a(0) + ")"
	case "float.tan", "*.tan":
		return "math.Tan(" + a(0) + ")"
	case "float.asin", "*.asin":
		return "math.Asin(" + a(0) + ")"
	case "float.acos", "*.acos":
		return "math.Acos(" + a(0) + ")"
	case "float.atan", "*.atan":
		return "math.Atan(" + a(0) + ")"
	case "float.atan2", "*.atan2":
		return "math.Atan2(" + a(0) + ", " + a(1) + ")"
	case "string.length", "*.length":
		return "len(" + a(0) + ")"
	case "string.upper", "*.upper":
		return "strings.ToUpper(" + a(0) + ")"
	case "string.lower", "*.lower":
		return "strings.ToLower(" + a(0) + ")"
	case "string.trim", "*.trim":
		return "strings.TrimSpace(" + a(0) + ")"
	case "string.replace", "*.replace":
		return "strings.ReplaceAll(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "string.indexOf", "*.indexOf":
		return "strings.Index(" + a(0) + ", " + a(1) + ")"
	case "string.substring", "*.substring":
		return a(0) + "[" + a(1) + ":" + a(2) + "]"
	case "list.length":
		return "len(" + a(0) + ")"
	case "list.push", "*.push":
		return a(0) + " = append(" + a(0) + ", " + a(1) + ")"
	case "list.remove", "*.remove":
		return a(0) + " = append(" + a(0) + "[:" + a(1) + "], " + a(0) + "[" + a(1) + "+1:]...)"
	case "list.join", "*.join":
		return "strings.Join(" + a(0) + ", " + a(1) + ")"
	case "list.filter", "*.filter":
		return "func() []any { var out []any; for _, item := range " + a(0) + " { if " + a(1) + ".(func(any) any)(item).(bool) { out = append(out, item) } }; return out }()"
	case "list.map", "*.map":
		return "func() []any { out := make([]any, len(" + a(0) + ")); for i, item := range " + a(0) + " { out[i] = " + a(1) + ".(func(any) any)(item) }; return out }()"
	// regex
	case "regex.matches":
		return a(0) + ".MatchString(" + a(1) + ")"
	case "regex.find", "*.find":
		return a(0) + ".FindString(" + a(1) + ")"
	// Alert
	case "Alert.toast":
		return `fmt.Println("[" + ` + a(1) + ` + "] " + ` + a(0) + `)`
	case "Alert.info":
		return `fmt.Println("[info] " + ` + a(0) + `)`
	case "Alert.warn":
		return `fmt.Println("[warn] " + ` + a(0) + `)`
	case "Alert.error":
		return `fmt.Println("[error] " + ` + a(0) + `)`
	case "Alert.confirm":
		return `true`
	// File
	case "File.pick":
		return `""`
	case "File.pickFolder":
		return `""`
	}
	return ""
}
