package kotlin

import (
	"strings"
	"sync"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func init() {
	codegen.RegisterNative("kotlin", &Translator{})
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
// keyword the way Kotlin does. passReservedNames renames a declaration that
// took one, so what reaches here is a name that is not the program's to
// change: a struct field, which a serialized record and a constructor's named
// arguments spell, and a native's parameter, which a host declared.
//
// The words are sngl:language/kotlin's own reservedWords default.
func SafeIdent(name string) string {
	if keywords()[name] {
		return "`" + name + "`"
	}
	return name
}

var keywords = sync.OnceValue(func() map[string]bool {
	set := map[string]bool{}
	for _, w := range codegen.LanguageReserves("kotlin") {
		set[w] = true
	}
	return set
})

// NewFileEmitter returns a Kotlin FileEmitter (see fileemit.go).
func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return newFileEmitter(sink, opts)
}

func exportName(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
