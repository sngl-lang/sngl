package golang

import (
	_ "embed"
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
func (t *Translator) Capabilities() lower.Features {
	f := lower.AllFeatures()
	// Go has no ternary expression; lower a ? b : c to an if/else with a temp var.
	f.Ternary = false
	// Go can't represent typed lambdas behind an interface{} surface
	// (no type-asserting a `func(int) bool`). Lower xs.filter/map to loops.
	f.ListLambdas = false
	return f
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return ExportName(name.Name)
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
	// Everything else is emitted by intrinsic id through the registry
	// (intrinsics.go). What is left here is i18n, whose wrappers rearrange
	// their arguments and so are not a pass-through of any single intrinsic.
	//
	// i18n — wrapper calls delegate to per-locale runtime entry points.
	// NoContext threads __ctx_locale as the trailing arg; we lift it to the
	// leading positional arg the runtime expects (i18n.<Foo>(locale, ...)).
	// Falls back to i18n.GetTranslator() (process-global) when no locale arg
	// was threaded — e.g. legacy callers reached before NoContext runs.
	case "i18n.tr":
		// Wrapper params: (key, args, __ctx_locale).
		if len(argExprs) >= 3 {
			return "i18n.Translate(" + a(2) + ", " + a(0) + ", \"\", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Tr(" + a(0) + ", " + a(0) + ", " + a(1) + ")"
	case "i18n.trInline":
		// Wrapper params: (key, inlinedTemplate, args, __ctx_locale).
		if len(argExprs) >= 4 {
			return "i18n.Translate(" + a(3) + ", " + a(0) + ", " + a(1) + ", " + a(2) + ")"
		}
		return "i18n.GetTranslator().Tr(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.format":
		// Wrapper params: (template, args, __ctx_locale).
		if len(argExprs) >= 3 {
			return "i18n.Format(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Format(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberInt":
		// Wrapper params: (n, style, __ctx_locale).
		if len(argExprs) >= 3 {
			return "i18n.NumberInt(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().NumberInt(" + a(0) + ", " + a(1) + ")"
	case "i18n.numberFloat":
		if len(argExprs) >= 3 {
			return "i18n.NumberFloat(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().NumberFloat(" + a(0) + ", " + a(1) + ")"
	case "i18n.date":
		if len(argExprs) >= 3 {
			return "i18n.Date(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Date(" + a(0) + ", " + a(1) + ")"
	case "i18n.time":
		if len(argExprs) >= 3 {
			return "i18n.Time(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Time(" + a(0) + ", " + a(1) + ")"
	case "i18n.datetime":
		// Wrapper params: (dt, dateStyle, timeStyle, __ctx_locale).
		if len(argExprs) >= 4 {
			return "i18n.Datetime(" + a(3) + ", " + a(0) + ", " + a(1) + ", " + a(2) + ")"
		}
		return "i18n.GetTranslator().Datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.select":
		if len(argExprs) >= 3 {
			return "i18n.Select(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Select(" + a(0) + ", " + a(1) + ")"
	case "i18n.plural":
		if len(argExprs) >= 3 {
			return "i18n.Plural(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Plural(" + a(0) + ", " + a(1) + ")"
	case "i18n.selectordinal":
		if len(argExprs) >= 3 {
			return "i18n.Selectordinal(" + a(2) + ", " + a(0) + ", " + a(1) + ")"
		}
		return "i18n.GetTranslator().Selectordinal(" + a(0) + ", " + a(1) + ")"
	case "i18n.defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return "i18n.DefaultLocale()"
	}
	return ""
}

// i18nPluralKeyGoName maps a SNGL plural-category name (zero, one, …, other) to
// the unqualified Go runtime constant name (PluralZero, PluralOne, …,
// PluralOther). Returns "" for unknown names.
func i18nPluralKeyGoName(snglName string) string {
	switch snglName {
	case "zero":
		return "PluralZero"
	case "one":
		return "PluralOne"
	case "two":
		return "PluralTwo"
	case "few":
		return "PluralFew"
	case "many":
		return "PluralMany"
	case "other":
		return "PluralOther"
	}
	return ""
}

// SnglI18nImportPath is the Go import path of git.duckfam.us/jonathan/sngl/pkg/go/i18n.
// Platform generators should add this import when PackageUsesI18n returns true.
const SnglI18nImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/i18n"

// PackageUsesI18n reports whether any function or component in pkg
// contains an i18n stdlib call. Platform generators use this to decide
// whether to add the SNGL i18n runtime import. The answer is stamped onto
// the package by the StampUsage lowering pass (which uses the same
// call + plural-key-Select predicate this used to compute inline).
func PackageUsesI18n(pkg *ir.Package) bool {
	return pkg != nil && pkg.UsesI18n
}

// NewFileEmitter returns a Go FileEmitter (see fileemit.go).
func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return newFileEmitter(sink, opts)
}
