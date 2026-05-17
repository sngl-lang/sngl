package golang

import (
	_ "embed"
	"fmt"
	"io"
	"slices"
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
func (t *Translator) Capabilities() lower.Caps {
	return lower.Caps{
		// Go can't represent typed lambdas behind an interface{} surface
		// (no type-asserting a `func(int) bool`). Lower xs.filter(f) /
		// xs.map(f) into an explicit accumulator + for-loop so codegen
		// only sees direct lambda calls with their concrete types.
		NoListLambdas: true,
	}
}

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
	// i18n — all calls delegate to i18n.GetTranslator() runtime.
	// For type-attached method dispatch, args are positional (no receiver value).
	// The NoContext lowering pass appends the active locale as a trailing named
	// arg (__ctx_locale) which appears positionally at the end of argExprs.
	case "i18n.tr":
		// Args: key string, args map[string]any, locale string (from lowering)
		return "i18n.GetTranslator().Tr(" + a(0) + ", " + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.format":
		// Args: template string, args map[string]any, locale string
		return "i18n.GetTranslator().Format(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.numberInt":
		// Args: n int, style string, locale string
		return "i18n.GetTranslator().NumberInt(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.numberFloat":
		// Args: n float, style string, locale string
		return "i18n.GetTranslator().NumberFloat(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.date":
		// Args: d date, style string, locale string
		return "i18n.GetTranslator().Date(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.time":
		// Args: t time, style string, locale string
		return "i18n.GetTranslator().Time(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.datetime":
		// Args: dt dateTime, dateStyle string, timeStyle string, locale string
		return "i18n.GetTranslator().Datetime(" + a(0) + ", " + a(1) + ", " + a(2) + ", " + a(3) + ")"
	case "i18n.select":
		// Args: value string, cases map[string]string, locale string
		return "i18n.GetTranslator().Select(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.plural":
		// Args: count, forms, locale string
		return "i18n.GetTranslator().Plural(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.selectordinal":
		// Args: count, forms, locale string
		return "i18n.GetTranslator().Selectordinal(" + a(0) + ", " + a(1) + ", " + a(2) + ")"
	case "i18n.exactly":
		// Args: n. a(0)=n. (no locale arg — exactly() is locale-independent)
		return "i18n.Exactly(" + a(0) + ")"
	case "i18n.defaultLocale":
		// No args. Returns the process-startup BCP-47 locale string.
		return "i18n.DefaultLocale()"
	}
	return ""
}

// IsI18nCall reports whether a qualified method name is an i18n stdlib call.
// Used by platform codegens to detect when the generated code needs to import
// git.duckfam.us/jonathan/sngl/pkg/go/i18n.
func IsI18nCall(qualName string) bool {
	switch qualName {
	case "i18n.tr", "i18n.format",
		"i18n.numberInt", "i18n.numberFloat",
		"i18n.date", "i18n.time", "i18n.datetime",
		"i18n.select",
		"i18n.plural", "i18n.selectordinal", "i18n.exactly",
		"i18n.defaultLocale":
		return true
	}
	return false
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

// PackageUsesI18n reports whether any function or component in pkg contains an
// i18n stdlib call. Platform generators use this to decide whether to add an
// i18n import to the generated Go file.
func PackageUsesI18n(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}
	if slices.ContainsFunc(pkg.Funcs, funcUsesI18n) {
		return true
	}
	for _, comp := range pkg.Components {
		if slices.ContainsFunc(comp.Funcs, funcUsesI18n) {
			return true
		}
		for _, v := range comp.Vars {
			for _, h := range v.Handlers {
				if h.Func != nil && funcUsesI18n(h.Func) {
					return true
				}
			}
			if exprUsesI18n(v.Init) {
				return true
			}
		}
		// Visual tree: i18n.tr calls live inside prop values of
		// `text(value=$"...")`, `button(text=$"...")`, etc.
		if slices.ContainsFunc(comp.Body, stmtUsesI18n) {
			return true
		}
	}
	for _, w := range pkg.Windows {
		if slices.ContainsFunc(w.Body, stmtUsesI18n) {
			return true
		}
	}
	for _, v := range pkg.Vars {
		for _, h := range v.Handlers {
			if h.Func != nil && funcUsesI18n(h.Func) {
				return true
			}
		}
		if exprUsesI18n(v.Init) {
			return true
		}
	}
	return false
}

func funcUsesI18n(f *ir.Func) bool {
	if f == nil {
		return false
	}
	return slices.ContainsFunc(f.Block, stmtUsesI18n)
}

func stmtUsesI18n(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.Return:
		return exprUsesI18n(n.Value)
	case *ir.Assign:
		return exprUsesI18n(n.Value)
	case *ir.LocalVar:
		return exprUsesI18n(n.Init)
	case *ir.CallStmt:
		return exprUsesI18n(n.Call)
	case *ir.If:
		if exprUsesI18n(n.Cond) {
			return true
		}
		if slices.ContainsFunc(n.Body, stmtUsesI18n) {
			return true
		}
		if slices.ContainsFunc(n.Else, stmtUsesI18n) {
			return true
		}
	case *ir.For:
		if exprUsesI18n(n.Iter) {
			return true
		}
		if slices.ContainsFunc(n.Body, stmtUsesI18n) {
			return true
		}
	case *ir.NodeInst:
		for _, prop := range n.Props {
			if exprUsesI18n(prop.Value) {
				return true
			}
		}
		for _, h := range n.Handlers {
			if h.Func != nil && funcUsesI18n(h.Func) {
				return true
			}
		}
		if slices.ContainsFunc(n.Children, stmtUsesI18n) {
			return true
		}
	}
	return false
}

func exprUsesI18n(e ir.Expr) bool {
	if e == nil {
		return false
	}
	switch n := e.(type) {
	case *ir.Call:
		if n.Func != nil && n.Func.Receiver == "i18n" {
			return true
		}
		if exprUsesI18n(n.Receiver) {
			return true
		}
		for _, a := range n.Args {
			if exprUsesI18n(a.Value) {
				return true
			}
		}
	case *ir.Binary:
		return exprUsesI18n(n.Left) || exprUsesI18n(n.Right)
	case *ir.Unary:
		return exprUsesI18n(n.Operand)
	case *ir.Ternary:
		return exprUsesI18n(n.Cond) || exprUsesI18n(n.Then) || exprUsesI18n(n.Else)
	case *ir.Select:
		// Detect i18n.zero / i18n.one / … / i18n.other as keys in plural maps.
		if ident, ok := n.Operand.(*ir.Ident); ok && ident.Name == "i18n" {
			if i18nPluralKeyGoName(n.Field) != "" {
				return true
			}
		}
		return exprUsesI18n(n.Operand)
	case *ir.MapLitIR:
		for _, entry := range n.Entries {
			if exprUsesI18n(entry.Key) || exprUsesI18n(entry.Value) {
				return true
			}
		}
	case *ir.ListLit:
		if slices.ContainsFunc(n.Elems, exprUsesI18n) {
			return true
		}
	case *ir.Index:
		return exprUsesI18n(n.Operand) || exprUsesI18n(n.Idx)
	case *ir.Conversion:
		return exprUsesI18n(n.Operand)
	case *ir.Lambda:
		return funcUsesI18n(n.Func)
	}
	return false
}
