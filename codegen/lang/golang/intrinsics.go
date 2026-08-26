package golang

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// langGo is this backend's registry key (matches LanguageIdentifier).
const langGo = "go"

// init registers the Go emitter for the intrinsics this backend implements,
// dispatched by ID (see codegen.EmitIntrinsicCall). Emitters declare the
// imports their native form needs ("strings"/"math"); GoIRContext applies them
// via RequireImport.
func init() {
	// reg registers an import-free emitter (builtins / slicing).
	reg := func(id string, fn func(a []string) string) {
		regImp(id, nil, fn)
	}

	// --- string ---
	// SNGL strings have rune (Unicode code point) semantics, not byte
	// semantics (bugs.md #16), so length counts runes and substring slices by
	// rune index — matching the interpreter and const folder.
	regImp("string.length", []string{"unicode/utf8"}, func(a []string) string { return "utf8.RuneCountInString(" + a[0] + ")" })
	regImp("string.indexOf", []string{"strings"}, func(a []string) string { return "strings.Index(" + a[0] + ", " + a[1] + ")" })
	reg("string.substring", func(a []string) string { return "string([]rune(" + a[0] + ")[" + a[1] + ":" + a[2] + "])" })
	// strconv.ParseInt returns (int64, error) and int.parse returns an int, so
	// there is no expression form without a func literal to drop the error.
	regImp("int.parse", []string{"strconv"}, func(a []string) string {
		return "func() int { __v, _ := strconv.ParseInt(" + a[0] + ", " + a[1] + ", 64); return int(__v) }()"
	})
	regImp("string.upper", []string{"strings"}, func(a []string) string { return "strings.ToUpper(" + a[0] + ")" })
	regImp("string.lower", []string{"strings"}, func(a []string) string { return "strings.ToLower(" + a[0] + ")" })
	regImp("string.trim", []string{"strings"}, func(a []string) string { return "strings.TrimSpace(" + a[0] + ")" })
	regImp("string.replace", []string{"strings"}, func(a []string) string { return "strings.ReplaceAll(" + a[0] + ", " + a[1] + ", " + a[2] + ")" })
	regImp("string.split", []string{"strings"}, func(a []string) string { return "strings.Split(" + a[0] + ", " + a[1] + ")" })

	// --- float math --- (Math{Floor,Ceil,Round} return int)
	regImp("float.floor", []string{"math"}, func(a []string) string { return "math.Floor(" + a[0] + ")" })
	regImp("float.ceil", []string{"math"}, func(a []string) string { return "math.Ceil(" + a[0] + ")" })
	regImp("float.round", []string{"math"}, func(a []string) string { return "math.Round(" + a[0] + ")" })
	regImp("float.sqrt", []string{"math"}, func(a []string) string { return "math.Sqrt(" + a[0] + ")" })
	regImp("float.pow", []string{"math"}, func(a []string) string { return "math.Pow(" + a[0] + ", " + a[1] + ")" })
	regImp("float.sin", []string{"math"}, func(a []string) string { return "math.Sin(" + a[0] + ")" })
	regImp("float.cos", []string{"math"}, func(a []string) string { return "math.Cos(" + a[0] + ")" })
	regImp("float.tan", []string{"math"}, func(a []string) string { return "math.Tan(" + a[0] + ")" })
	regImp("float.asin", []string{"math"}, func(a []string) string { return "math.Asin(" + a[0] + ")" })
	regImp("float.acos", []string{"math"}, func(a []string) string { return "math.Acos(" + a[0] + ")" })
	regImp("float.atan", []string{"math"}, func(a []string) string { return "math.Atan(" + a[0] + ")" })
	regImp("float.atan2", []string{"math"}, func(a []string) string { return "math.Atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list ---
	reg("list.length", func(a []string) string { return "len(" + a[0] + ")" })

	// --- list (in-place mutations) ---
	// ListPush/ListRemove mutate the receiver slice in place and return it;
	// `append` reassigns the receiver, matching the mutates-receiver semantics
	// the checker and reactivity rely on. No import (append is a builtin).
	reg("list.push", func(a []string) string {
		return a[0] + " = append(" + a[0] + ", " + a[1] + ")"
	})
	// An out-of-range index leaves the list unchanged (lib/builtin/methods.sngl).
	// The index is bound once: it is an arbitrary expression and the guard
	// reads it three times.
	reg("list.remove", func(a []string) string {
		return "if __i := " + a[1] + "; __i >= 0 && __i < len(" + a[0] + ") { " +
			a[0] + " = append(" + a[0] + "[:__i], " + a[0] + "[__i+1:]...) }"
	})

	// --- int/float min, max, abs, clamp ---
	// Their SNGL bodies are correct but spell the answer as a ternary, which
	// Go has no expression form for; these are the reason the `usable` flag
	// distinguishes "a backend may substitute" from "a backend must".
	reg("int.min", func(a []string) string { return "min(" + a[0] + ", " + a[1] + ")" })
	reg("int.max", func(a []string) string { return "max(" + a[0] + ", " + a[1] + ")" })
	reg("int.abs", func(a []string) string {
		return "func(x int) int { if x < 0 { return -x }; return x }(" + a[0] + ")"
	})
	// max(lo, min(x, hi)): with a malformed range lo wins, which is what the
	// declaration documents and its own body computes.
	reg("int.clamp", func(a []string) string {
		return "max(" + a[1] + ", min(" + a[0] + ", " + a[2] + "))"
	})
	regImp("float.min", []string{"math"}, func(a []string) string { return "math.Min(" + a[0] + ", " + a[1] + ")" })
	regImp("float.max", []string{"math"}, func(a []string) string { return "math.Max(" + a[0] + ", " + a[1] + ")" })
	regImp("float.abs", []string{"math"}, func(a []string) string { return "math.Abs(" + a[0] + ")" })
	regImp("float.clamp", []string{"math"}, func(a []string) string {
		return "math.Max(" + a[1] + ", math.Min(" + a[0] + ", " + a[2] + "))"
	})

	// --- list and map ---
	// Go is statically typed, so these name the element type they build rather
	// than falling back to []any: a list<int> is emitted as []int, and an []any
	// result would not assign to it.
	// strings.Join needs []string, so it only spells a list<string>. Every
	// other element type goes through the platform's default conversion, which
	// is what the declaration promises and what fmt.Sprint is.
	regTypedImp("list.join", func(a []string, ts []*ir.Type) (string, []string) {
		if goElem(ts[0]) == "string" {
			return "strings.Join(" + a[0] + ", " + a[1] + ")", []string{"strings"}
		}
		return "func() string { __src := " + a[0] + "; __parts := make([]string, len(__src)); " +
			"for __i, __v := range __src { __parts[__i] = fmt.Sprint(__v) }; " +
			"return strings.Join(__parts, " + a[1] + ") }()", []string{"strings", "fmt"}
	})
	regImp("list.indexOf", []string{"slices"}, func(a []string) string { return "slices.Index(" + a[0] + ", " + a[1] + ")" })
	// A Go slice expression aliases its operand and panics out of range; slice
	// is documented to copy and to clamp both bounds to [0, length].
	regTyped("list.slice", nil, func(a []string, ts []*ir.Type) string {
		el := goElem(ts[0])
		return "func() []" + el + " { __src := " + a[0] + "; __n := len(__src); " +
			"__lo, __hi := " + a[1] + ", " + a[2] + "; " +
			"if __lo < 0 { __lo = 0 }; if __lo > __n { __lo = __n }; " +
			"if __hi < 0 { __hi = 0 }; if __hi > __n { __hi = __n }; " +
			"if __hi < __lo { __hi = __lo }; " +
			"__out := make([]" + el + ", __hi-__lo); copy(__out, __src[__lo:__hi]); return __out }()"
	})
	regTyped("list.reverse", nil, func(a []string, ts []*ir.Type) string {
		el := goElem(ts[0])
		return "func() []" + el + " { __src := " + a[0] + "; __out := make([]" + el + ", len(__src)); for __i, __v := range __src { __out[len(__src)-1-__i] = __v }; return __out }()"
	})
	regTyped("list.filter", nil, func(a []string, ts []*ir.Type) string {
		el := goElem(ts[0])
		return "func() []" + el + " { var __out []" + el + "; for _, __item := range " + a[0] + " { if " + a[1] + "(__item) { __out = append(__out, __item) } }; return __out }()"
	})
	regTyped("list.map", nil, func(a []string, ts []*ir.Type) string {
		// The result element type is the mapper's return type, not the source's.
		out := "any"
		if len(ts) > 1 && ts[1] != nil && ts[1].Sig != nil && ts[1].Sig.Return != nil {
			out = IRTypeToGo(ts[1].Sig.Return)
		}
		return "func() []" + out + " { __out := make([]" + out + ", len(" + a[0] + ")); for __i, __item := range " + a[0] + " { __out[__i] = " + a[1] + "(__item) }; return __out }()"
	})
	reg("map.length", func(a []string) string { return "len(" + a[0] + ")" })
	regTyped("map.keys", nil, func(a []string, ts []*ir.Type) string {
		k := goKey(ts[0])
		return "func() []" + k + " { __ks := make([]" + k + ", 0, len(" + a[0] + ")); for __k := range " + a[0] + " { __ks = append(__ks, __k) }; return __ks }()"
	})
	regTyped("map.values", nil, func(a []string, ts []*ir.Type) string {
		v := goVal(ts[0])
		return "func() []" + v + " { __vs := make([]" + v + ", 0, len(" + a[0] + ")); for _, __v := range " + a[0] + " { __vs = append(__vs, __v) }; return __vs }()"
	})
	reg("map.contains", func(a []string) string {
		return "func() bool { _, __ok := " + a[0] + "[" + a[1] + "]; return __ok }()"
	})
	regTyped("map.get", nil, func(a []string, ts []*ir.Type) string {
		v := goVal(ts[0])
		return "func() " + v + " { if __v, __ok := " + a[0] + "[" + a[1] + "]; __ok { return __v }; return " + a[2] + " }()"
	})

	regImp("i18n.exactly", []string{SnglI18nImportPath}, func(a []string) string {
		return "i18n.Exactly(" + a[0] + ")"
	})

	// --- color ---
	regImp("color.hex", []string{colorImportPath}, func(a []string) string { return a[0] + ".Hex()" })

	// --- File ---
	// Alert.* is deliberately absent: GoIRContext.evalAlertCall owns it, so a
	// platform can replace it (gtk4 writes to os.Stderr) and the default
	// appends to m.toasts. An emitter here would win over both — the platform
	// would keep declaring the "os" import for a call that no longer used it.
	reg("File.pick", func(a []string) string { return `""` })
	reg("File.pickFolder", func(a []string) string { return `""` })

	// --- intl (locale-aware formatting) ---
	// Each maps to the same-named entry point in the Go i18n runtime; the
	// locale is already the leading argument by the time a call gets here.
	for id, goName := range map[string]string{
		"i18n._defaultLocale": "DefaultLocale", "i18n._translate": "Translate", "i18n._format": "Format",
		"i18n._numberInt": "NumberInt", "i18n._numberFloat": "NumberFloat", "i18n._date": "Date",
		"i18n._time": "Time", "i18n._dateTime": "Datetime", "i18n._select": "Select",
		"i18n._plural": "Plural", "i18n._selectOrdinal": "Selectordinal",
	} {
		call := "i18n." + goName
		regImp(id, []string{SnglI18nImportPath}, func(a []string) string {
			return call + "(" + strings.Join(a, ", ") + ")"
		})
	}

	// --- i18n entry points ---
	// Dispatched by id: a package function carries no receiver, so the
	// qualified name these were matched by is not there after lowering.
	for _, name := range []string{"defaultLocale", "tr", "trInline", "format", "numberInt", "numberFloat", "date", "time", "datetime", "select", "plural", "selectordinal"} {
		id := "i18n." + name
		qual := id
		regImp(id, []string{SnglI18nImportPath}, func(a []string) string {
			return goBuiltinMethodFromArgs(qual, a)
		})
	}

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("html.frontend", func(a []string) string { return a[0] })
	reg("html.backend", func(a []string) string { return a[0] })
}

// regTyped registers an emitter that needs its arguments' types, not only
// their emitted text. Go names every type it builds, so a list or map helper
// has to spell the element type out.
func regTyped(id string, imports []string, fn func(a []string, ts []*ir.Type) string) {
	codegen.RegisterIntrinsic(langGo, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		a := make([]string, len(args))
		ts := make([]*ir.Type, len(args))
		for i, e := range args {
			a[i] = tr(e)
			if e != nil {
				ts[i] = e.ExprType()
			}
		}
		return fn(a, ts), imports
	})
}

// regTypedImp registers an emitter whose imports depend on the argument types
// it was given, so a form that only some element types take does not declare an
// import the others would leave unused.
func regTypedImp(id string, fn func(a []string, ts []*ir.Type) (string, []string)) {
	codegen.RegisterIntrinsic(langGo, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		a := make([]string, len(args))
		ts := make([]*ir.Type, len(args))
		for i, e := range args {
			a[i] = tr(e)
			if e != nil {
				ts[i] = e.ExprType()
			}
		}
		return fn(a, ts)
	})
}

// goElem, goKey and goVal name a container's parts in Go. An unresolved type
// degrades to any rather than emitting nothing: the result still compiles when
// the surrounding context is itself dynamic.
func goElem(t *ir.Type) string {
	if t == nil || len(t.Elems) < 1 {
		return "any"
	}
	return IRTypeToGo(t.Elems[0])
}

func goKey(t *ir.Type) string { return goElem(t) }

func goVal(t *ir.Type) string {
	if t == nil || len(t.Elems) < 2 {
		return "any"
	}
	return IRTypeToGo(t.Elems[1])
}

// regImp registers an emitter that declares the given Go import paths.
func regImp(id string, imports []string, fn func(a []string) string) {
	codegen.RegisterIntrinsic(langGo, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		a := make([]string, len(args))
		for i, e := range args {
			a[i] = tr(e)
		}
		return fn(a), imports
	})
}
