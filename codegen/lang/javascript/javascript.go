package javascript

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

// Translator implements codegen.LangTranslator for JavaScript.
type Translator struct{}

func (t *Translator) LanguageIdentifier() string          { return "js" }
func (t *Translator) Description() string                 { return "Generate JavaScript source." }
func (t *Translator) Package() []*ast.Document            { return nil }
func (t *Translator) Resolve(identifier string) ir.Symbol { return nil }
func (t *Translator) Capabilities() lower.Caps            { return lower.Caps{} }

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return name.Name
}

// TranslateIRExpr translates an IR expression directly to its JS form.
func (t *Translator) TranslateIRExpr(e ir.Expr, scope *codegen.ExprScope) string {
	return translateIRExpr(e, scope)
}

// TranslateIRMutation translates an IR mutation statement to JS statements.
func (t *Translator) TranslateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string {
	return translateIRMutation(s, scope)
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
				parts = append(parts, "..."+t.TranslateIRLiteral(f.Value))
			} else {
				parts = append(parts, f.Name+": "+t.TranslateIRLiteral(f.Value))
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	// Best-effort fallback for non-literal Expr shapes (computed defaults,
	// unresolved expressions). Mirrors IRLiteralToGo — callers that hit
	// this arm get an empty string rather than a panic.
	return `""`
}

// RenderHeader returns the generated-by comment for JS files. Import lines
// will render here when platforms migrate to CodeWriter (Plan B).
func (t *Translator) RenderHeader(name, source string, imports []codegen.ImportSpec) []byte {
	return []byte(codegen.Header("js", source, "// ", ""))
}

// RenderSourceMap emits a source-map v3 sidecar and appends a sourceMappingURL
// footer to the body.
func (t *Translator) RenderSourceMap(name string, positions []codegen.PosEntry, body []byte) codegen.SourceMapResult {
	return renderJSSourceMap(name, positions, body)
}

// SnglI18nImportPath is the JS module specifier the generated bundle uses
// to import the i18n runtime. The html platform copies pkg/js/i18n/ into
// the output as a sibling of the entry bundle so this relative path
// resolves at runtime.
const SnglI18nImportPath = "./i18n/i18n.js"

// IsI18nCall reports whether a qualified method name is an i18n stdlib call.
// Mirrors the Go translator's helper of the same name. Platform codegens use
// this to decide whether to inject the i18n runtime import.
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

// jsEvalIntlIntrinsic emits JavaScript source for an intl.* intrinsic
// call. After NoContext + InlinePure, calls to i18n.* SNGL wrappers are
// inlined into their intrinsic targets with the active locale threaded
// as the first argument. The emitted call targets the per-locale entry
// points exported by pkg/js/i18n/i18n.js.
//
// Returns "" if fn isn't an intl intrinsic.
func jsEvalIntlIntrinsic(fn *ir.Func, args []string) string {
	if fn == nil || fn.Intrinsic == "" {
		return ""
	}
	join := func() string { return strings.Join(args, ", ") }
	switch fn.Intrinsic {
	case "DefaultLocale":
		return "i18n.defaultLocale()"
	case "Translate":
		return "i18n.translate(" + join() + ")"
	case "Format":
		return "i18n.format(" + join() + ")"
	case "NumberInt":
		return "i18n.numberInt(" + join() + ")"
	case "NumberFloat":
		return "i18n.numberFloat(" + join() + ")"
	case "Date":
		return "i18n.date(" + join() + ")"
	case "Time":
		return "i18n.time(" + join() + ")"
	case "DateTime":
		return "i18n.datetime(" + join() + ")"
	case "Select":
		return "i18n.select(" + join() + ")"
	case "Plural":
		return "i18n.plural(" + join() + ")"
	case "SelectOrdinal":
		return "i18n.selectordinal(" + join() + ")"
	}
	return ""
}

func (t *Translator) TypeToNative(hint string) string {
	if strings.HasPrefix(hint, "option:") {
		return t.TypeToNative(hint[7:]) // JS has no option types; everything is nullable
	}
	switch hint {
	case "int", "float":
		return "number"
	case "bool":
		return "boolean"
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
	if jsReservedWords[name] {
		return name + "_"
	}
	return name
}

// jsReservedWords lists JavaScript reserved words and contextually reserved
// words that cannot be used as bare identifiers. SNGL identifiers that
// collide get suffixed with `_`.
var jsReservedWords = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true,
	"const": true, "continue": true, "debugger": true, "default": true,
	"delete": true, "do": true, "else": true, "enum": true,
	"export": true, "extends": true, "false": true, "finally": true,
	"for": true, "function": true, "if": true, "import": true,
	"in": true, "instanceof": true, "new": true, "null": true,
	"return": true, "super": true, "switch": true, "this": true,
	"throw": true, "true": true, "try": true, "typeof": true,
	"var": true, "void": true, "while": true, "with": true,
	"yield": true, "let": true, "static": true, "implements": true,
	"interface": true, "package": true, "private": true,
	"protected": true, "public": true, "await": true, "async": true,
	"of": true, "as": true,
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

// isIntNode reports whether a SNGL node is known to produce an integer value.
func isIntNode(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return n.Kind == ast.LiteralInt
	case *ast.CallExpr:
		if ident, ok := n.Func.(*ast.IdentExpr); ok {
			return ident.Name == "int"
		}
		return false
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinAdd, ast.BinSub, ast.BinMul, ast.BinDiv, ast.BinMod:
			return isIntNode(n.Left) && isIntNode(n.Right)
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNeg {
			return isIntNode(n.Operand)
		}
	case *ast.ParenExpr:
		return isIntNode(n.Inner)
	}
	return false
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
