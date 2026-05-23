package kotlin

import (
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

// SnglI18nKotlinPackage is the fully-qualified Kotlin package name for the
// SNGL i18n runtime shipped in pkg/kotlin/i18n/.
const SnglI18nKotlinPackage = "us.duckfam.git.jonathan.sngl.i18n"

// IsI18nCall reports whether a qualified method name is an i18n stdlib call.
// Mirrors the JS and Go translators' helper of the same name. Platform
// codegens use this to decide whether to inject the i18n runtime import.
func IsI18nCall(qualName string) bool {
	switch qualName {
	case "i18n.tr", "i18n.trInline", "i18n.format",
		"i18n.numberInt", "i18n.numberFloat",
		"i18n.date", "i18n.time", "i18n.datetime",
		"i18n.select",
		"i18n.plural", "i18n.selectordinal", "i18n.exactly":
		return true
	}
	return false
}

// IsIntlIntrinsic reports whether intrinsic is one of the stdlib i18n
// intrinsics emitted by lib/i18n.sngl wrappers. After NoContext +
// InlinePure inlines the wrappers, callers see direct intrinsic Calls
// instead of i18n.* receiver calls.
func IsIntlIntrinsic(intrinsic string) bool {
	switch intrinsic {
	case "DefaultLocale", "Translate", "Format",
		"NumberInt", "NumberFloat",
		"Date", "Time", "DateTime",
		"Select", "Plural", "SelectOrdinal":
		return true
	}
	return false
}

// kotlinEvalIntlIntrinsic emits Kotlin source for an intl.* intrinsic call.
// Matches the structure of the Go runtime helper: maps the SNGL intrinsic
// name to the corresponding entry point on the I18n runtime object, with
// the locale threaded as the first argument.
//
// Returns "" if fn isn't an intl intrinsic.
func kotlinEvalIntlIntrinsic(fn *ir.Func, args []string) string {
	if fn == nil || fn.Intrinsic == "" {
		return ""
	}
	join := func() string { return strings.Join(args, ", ") }
	switch fn.Intrinsic {
	case "DefaultLocale":
		return "I18n.defaultLocale()"
	case "Translate":
		return "I18n.translate(" + join() + ")"
	case "Format":
		return "I18n.format(" + join() + ")"
	case "NumberInt":
		return "I18n.numberInt(" + join() + ")"
	case "NumberFloat":
		return "I18n.numberFloat(" + join() + ")"
	case "Date":
		return "I18n.date(" + join() + ")"
	case "Time":
		return "I18n.time(" + join() + ")"
	case "DateTime":
		return "I18n.datetime(" + join() + ")"
	case "Select":
		return "I18n.selectStr(" + join() + ")"
	case "Plural":
		return "I18n.plural(" + join() + ")"
	case "SelectOrdinal":
		return "I18n.selectordinal(" + join() + ")"
	}
	return ""
}

func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }
func (t *Translator) Capabilities() lower.Caps            { return lower.Caps{} }

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
}

func (t *Translator) TranslateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	return translateIRExpr(e, scope)
}

func (t *Translator) TranslateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	return translateIRMutation(s, scope)
}

func (t *Translator) TranslateIRLiteral(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Literal:
		return translateIRLiteral(n)
	case *ir.MapLitIR:
		return IRLiteralToKt(n)
	case *ir.ListLit:
		return IRLiteralToKt(n)
	}
	// Best-effort fallback for non-literal Expr shapes (computed defaults,
	// unresolved expressions). Mirrors IRLiteralToGo / TranslateIRLiteral
	// (js) — callers that hit this arm get an empty string rather than
	// a panic.
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
	if kotlinHardKeywords[name] {
		return "`" + name + "`"
	}
	return name
}

// RenderHeader returns the generated-by comment for Kotlin files. Import
// lines will render here when platforms migrate to CodeWriter (Plan B).
func (t *Translator) RenderHeader(name, source string, imports []codegen.ImportSpec) []byte {
	return []byte(codegen.Header("kt", source, "// ", ""))
}

// RenderSourceMap returns no changes. Kotlin SMAP support is deferred (spec
// section 7).
func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return codegen.SourceMapResult{}
}

// kotlinHardKeywords lists Kotlin hard keywords that cannot be used as bare
// identifiers. Colliding SNGL identifiers are wrapped in backticks (Kotlin's
// escape syntax).
var kotlinHardKeywords = map[string]bool{
	"as": true, "break": true, "class": true, "continue": true,
	"do": true, "else": true, "false": true, "for": true,
	"fun": true, "if": true, "in": true, "interface": true,
	"is": true, "null": true, "object": true, "package": true,
	"return": true, "super": true, "this": true, "throw": true,
	"true": true, "try": true, "typealias": true, "typeof": true,
	"val": true, "var": true, "when": true, "while": true,
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
