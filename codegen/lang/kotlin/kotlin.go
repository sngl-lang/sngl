package kotlin

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
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

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
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

func (t *Translator) ExportName(name string) string { return SafeIdent(name) }

// SafeIdent renders a SNGL identifier as a Kotlin one, escaping a hard
// keyword the way Kotlin does. A parameter or local named `object`, `is` or
// `when` is ordinary SNGL and not a Kotlin identifier at all, so every site
// that emits a declared name goes through here.
func SafeIdent(name string) string {
	if kotlinHardKeywords[name] {
		return "`" + name + "`"
	}
	return name
}

// NewFileEmitter returns a Kotlin FileEmitter (see fileemit.go).
func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return newFileEmitter(sink, opts)
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

func exportName(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
