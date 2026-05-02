package kotlin

import (
	"fmt"
	"io"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for Kotlin.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string { return "kotlin" }
func (t *Translator) Description() string {
	return "Generate Kotlin source (used with the Android platform)."
}
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }
func (t *Translator) Capabilities() lower.Caps            { return lower.Caps{} }

// v2 IR-based methods (stubs — will be implemented during platform migration).

func (t *Translator) WriteExpr(w io.Writer, expr ir.Expr, scope *ir.Scope) error {
	return fmt.Errorf("kotlin: WriteExpr not yet implemented")
}

func (t *Translator) WriteStmt(w io.Writer, stmt ir.Stmt, scope *ir.Scope) error {
	return fmt.Errorf("kotlin: WriteStmt not yet implemented")
}

func (t *Translator) WriteType(w io.Writer, typ *ir.Type) error {
	return fmt.Errorf("kotlin: WriteType not yet implemented")
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
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
		return t.TypeToNative(hint[7:]) + "?"
	}
	switch hint {
	case "int":
		return "Int"
	case "float":
		return "Double"
	case "bool":
		return "Boolean"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "String"
	default:
		return "Any"
	}
}

func (t *Translator) ExportName(name string) string {
	return name
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

func exportName(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
