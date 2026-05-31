package golang

import (
	_ "embed"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
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
func (t *Translator) Capabilities() lower.Caps {
	return lower.Caps{
		// Go can't represent typed lambdas behind an interface{} surface
		// (no type-asserting a `func(int) bool`). Lower xs.filter(f) /
		// xs.map(f) into an explicit accumulator + for-loop so codegen
		// only sees direct lambda calls with their concrete types.
		NoListLambdas: true,
	}
}

func (t *Translator) GenerateIdentifier(name *ir.Ident) string {
	return ExportName(name.Name)
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
	case "list.join", "*.join":
		return "strings.Join(" + a(0) + ", " + a(1) + ")"
	case "list.filter", "*.filter":
		return "func() []any { var out []any; for _, item := range " + a(0) + " { if " + a(1) + ".(func(any) any)(item).(bool) { out = append(out, item) } }; return out }()"
	case "list.map", "*.map":
		return "func() []any { out := make([]any, len(" + a(0) + ")); for i, item := range " + a(0) + " { out[i] = " + a(1) + ".(func(any) any)(item) }; return out }()"
	// map
	case "map.length":
		return "len(" + a(0) + ")"
	case "map.keys":
		return "func() []any { ks := make([]any, 0, len(" + a(0) + ")); for k := range " + a(0) + " { ks = append(ks, k) }; return ks }()"
	case "map.values":
		return "func() []any { vs := make([]any, 0, len(" + a(0) + ")); for _, v := range " + a(0) + " { vs = append(vs, v) }; return vs }()"
	case "map.contains":
		return "func() bool { _, ok := " + a(0) + "[" + a(1) + "]; return ok }()"
	case "map.get":
		return "func() any { if v, ok := " + a(0) + "[" + a(1) + "]; ok { return v }; return " + a(2) + " }()"
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
	case "i18n.exactly":
		// Args: n. a(0)=n.
		return "i18n.Exactly(" + a(0) + ")"
	case "i18n.defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return "i18n.DefaultLocale()"
	}
	return ""
}

// goEvalIntlIntrinsic emits Go source for an intl.* intrinsic call. After
// NoContext + InlinePure, calls to i18n.* SNGL wrappers are inlined into
// their intrinsic targets (intl.Translate, intl.NumberInt, …) with the
// active locale threaded as the first argument. The emitted call targets
// the per-locale entry points in pkg/go/i18n which route into a cached
// locale-keyed Translator.
//
// Returns "" if fn isn't an intl intrinsic.
func goEvalIntlIntrinsic(fn *ir.Func, args []string) string {
	if fn == nil || fn.Intrinsic == "" {
		return ""
	}
	join := func() string { return strings.Join(args, ", ") }
	switch fn.Intrinsic {
	case "DefaultLocale":
		return "i18n.DefaultLocale()"
	case "Translate":
		return "i18n.Translate(" + join() + ")"
	case "Format":
		return "i18n.Format(" + join() + ")"
	case "NumberInt":
		return "i18n.NumberInt(" + join() + ")"
	case "NumberFloat":
		return "i18n.NumberFloat(" + join() + ")"
	case "Date":
		return "i18n.Date(" + join() + ")"
	case "Time":
		return "i18n.Time(" + join() + ")"
	case "DateTime":
		return "i18n.Datetime(" + join() + ")"
	case "Select":
		return "i18n.Select(" + join() + ")"
	case "Plural":
		return "i18n.Plural(" + join() + ")"
	case "SelectOrdinal":
		return "i18n.Selectordinal(" + join() + ")"
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
// contains an i18n stdlib call. Platform generators use this to
// decide whether to add the SNGL i18n runtime import. Driven by
// the shared ir.WalkExprs + snglI18n.IsCall; also catches the
// i18n.zero/one/…/other plural-key Selects that survive as bare
// idents in plural-map literals.
func PackageUsesI18n(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) bool {
		switch n := e.(type) {
		case *ir.Call:
			if snglI18n.IsCall(n) {
				found = true
				return true
			}
		case *ir.Select:
			if ident, ok := n.Operand.(*ir.Ident); ok && ident.Name == "i18n" {
				if i18nPluralKeyGoName(n.Field) != "" {
					found = true
					return true
				}
			}
		}
		return false
	})
	return found
}

// NewFileEmitter returns a Go FileEmitter (see fileemit.go).
func (t *Translator) NewFileEmitter(sink codegen.Sink, opts codegen.FileOptions) codegen.FileEmitter {
	return newFileEmitter(sink, opts)
}
