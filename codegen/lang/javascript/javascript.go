package javascript

import (
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterNative("js", &Translator{})
}

// Translator implements codegen.LangTranslator for JavaScript.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string { return "js" }
func (t *Translator) Description() string        { return "Generate JavaScript source." }

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
}

// TranslateIRLiteral translates an IR literal expression to its JS literal.
// Handles scalar literals, list literals, and struct literals — anything
// `codegen.IRIsLiteral` reports true for.
func (t *Translator) TranslateIRLiteral(e ir.Expr) string {
	switch n := e.(type) {
	case *ir.Literal:
		return translateIRLiteral(n)
	case *ir.ListLit:
		parts := make([]string, len(n.Elems))
		for i, el := range n.Elems {
			parts[i] = t.TranslateIRLiteral(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *ir.MapLitIR:
		var b strings.Builder
		b.WriteString("new Map([")
		for i, e := range n.Entries {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("[")
			b.WriteString(t.TranslateIRLiteral(e.Key))
			b.WriteString(", ")
			b.WriteString(t.TranslateIRLiteral(e.Value))
			b.WriteString("]")
		}
		b.WriteString("])")
		return b.String()
	case *ir.StructLit:
		parts := make([]string, 0, len(n.Fields))
		for _, f := range n.Fields {
			if f.Spread {
				panic("javascript: struct spread must be lowered by flatten_struct_spread")
			}
			parts = append(parts, f.Name+": "+t.TranslateIRLiteral(f.Value))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	// Best-effort fallback for non-literal Expr shapes (computed defaults,
	// unresolved expressions). Mirrors IRLiteralToGo — callers that hit
	// this arm get an empty string rather than a panic.
	return `""`
}

// NewFileEmitter returns a JS FileEmitter (see fileemit.go).
func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return newFileEmitter(sink, opts)
}

// SnglI18nImportPath is the JS module specifier the generated bundle uses
// to import the i18n runtime. The html platform copies pkg/js/i18n/ into
// the output as a sibling of the entry bundle so this relative path
// resolves at runtime.
const SnglI18nImportPath = "./i18n/i18n.js"

func (t *Translator) ExportName(name string) string {
	return SafeIdent(name)
}

// SafeIdent is name as a JavaScript binding: suffixed when the language
// reserves it. passReservedNames renames a declaration that took a reserved
// word, so what reaches here is a name that is not the program's to change --
// a struct field, bound as its constructor's parameter.
//
// The words are sngl:language/js's own `#[gen.reserves]`.
func SafeIdent(name string) string {
	if words()[name] {
		return name + "_"
	}
	return name
}

var words = sync.OnceValue(func() map[string]bool {
	set := map[string]bool{}
	for _, w := range codegen.LanguageReserves("js") {
		set[w] = true
	}
	return set
})

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
		return "==="
	case ast.BinNeq:
		return "!=="
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
