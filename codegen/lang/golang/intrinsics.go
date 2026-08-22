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
// via RequireImport. Emissions match what goBuiltinMethodFromArgs produced for
// the corresponding method qualNames.
func init() {
	// reg registers an import-free emitter (builtins / slicing).
	reg := func(id string, fn func(a []string) string) {
		regImp(id, nil, fn)
	}

	// --- string ---
	// SNGL strings have rune (Unicode code point) semantics, not byte
	// semantics (bugs.md #16), so length counts runes and substring slices by
	// rune index — matching the interpreter and const folder.
	regImp("StrLength", []string{"unicode/utf8"}, func(a []string) string { return "utf8.RuneCountInString(" + a[0] + ")" })
	regImp("StrIndexOf", []string{"strings"}, func(a []string) string { return "strings.Index(" + a[0] + ", " + a[1] + ")" })
	reg("StrSubstring", func(a []string) string { return "string([]rune(" + a[0] + ")[" + a[1] + ":" + a[2] + "])" })
	// strconv.ParseInt returns (int64, error) and int.parse returns an int, so
	// there is no expression form without a func literal to drop the error.
	regImp("IntParse", []string{"strconv"}, func(a []string) string {
		return "func() int { v, _ := strconv.ParseInt(" + a[0] + ", " + a[1] + ", 64); return int(v) }()"
	})
	regImp("StrUpper", []string{"strings"}, func(a []string) string { return "strings.ToUpper(" + a[0] + ")" })
	regImp("StrLower", []string{"strings"}, func(a []string) string { return "strings.ToLower(" + a[0] + ")" })
	regImp("StrTrim", []string{"strings"}, func(a []string) string { return "strings.TrimSpace(" + a[0] + ")" })
	regImp("StrReplace", []string{"strings"}, func(a []string) string { return "strings.ReplaceAll(" + a[0] + ", " + a[1] + ", " + a[2] + ")" })
	regImp("StrSplit", []string{"strings"}, func(a []string) string { return "strings.Split(" + a[0] + ", " + a[1] + ")" })

	// --- float math --- (Math{Floor,Ceil,Round} return int)
	regImp("MathFloor", []string{"math"}, func(a []string) string { return "int(math.Floor(" + a[0] + "))" })
	regImp("MathCeil", []string{"math"}, func(a []string) string { return "int(math.Ceil(" + a[0] + "))" })
	regImp("MathRound", []string{"math"}, func(a []string) string { return "int(math.Round(" + a[0] + "))" })
	regImp("MathSqrt", []string{"math"}, func(a []string) string { return "math.Sqrt(" + a[0] + ")" })
	regImp("MathPow", []string{"math"}, func(a []string) string { return "math.Pow(" + a[0] + ", " + a[1] + ")" })
	regImp("MathSin", []string{"math"}, func(a []string) string { return "math.Sin(" + a[0] + ")" })
	regImp("MathCos", []string{"math"}, func(a []string) string { return "math.Cos(" + a[0] + ")" })
	regImp("MathTan", []string{"math"}, func(a []string) string { return "math.Tan(" + a[0] + ")" })
	regImp("MathAsin", []string{"math"}, func(a []string) string { return "math.Asin(" + a[0] + ")" })
	regImp("MathAcos", []string{"math"}, func(a []string) string { return "math.Acos(" + a[0] + ")" })
	regImp("MathAtan", []string{"math"}, func(a []string) string { return "math.Atan(" + a[0] + ")" })
	regImp("MathAtan2", []string{"math"}, func(a []string) string { return "math.Atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list ---
	reg("ListLength", func(a []string) string { return "len(" + a[0] + ")" })

	// --- list (in-place mutations) ---
	// ListPush/ListRemove mutate the receiver slice in place and return it;
	// `append` reassigns the receiver, matching the mutates-receiver semantics
	// the checker and reactivity rely on. No import (append is a builtin).
	reg("ListPush", func(a []string) string {
		return a[0] + " = append(" + a[0] + ", " + a[1] + ")"
	})
	reg("ListRemove", func(a []string) string {
		return a[0] + " = append(" + a[0] + "[:" + a[1] + "], " + a[0] + "[" + a[1] + "+1:]...)"
	})

	// --- int/float min, max, abs, clamp ---
	// Their SNGL bodies are correct but spell the answer as a ternary, which
	// Go has no expression form for; these are the reason the `usable` flag
	// distinguishes "a backend may substitute" from "a backend must".
	reg("IntMin", func(a []string) string { return "min(" + a[0] + ", " + a[1] + ")" })
	reg("IntMax", func(a []string) string { return "max(" + a[0] + ", " + a[1] + ")" })
	reg("IntAbs", func(a []string) string {
		return "func(x int) int { if x < 0 { return -x }; return x }(" + a[0] + ")"
	})
	reg("IntClamp", func(a []string) string {
		return "min(max(" + a[0] + ", " + a[1] + "), " + a[2] + ")"
	})
	regImp("FloatMin", []string{"math"}, func(a []string) string { return "math.Min(" + a[0] + ", " + a[1] + ")" })
	regImp("FloatMax", []string{"math"}, func(a []string) string { return "math.Max(" + a[0] + ", " + a[1] + ")" })
	regImp("FloatAbs", []string{"math"}, func(a []string) string { return "math.Abs(" + a[0] + ")" })
	regImp("FloatClamp", []string{"math"}, func(a []string) string {
		return "math.Min(math.Max(" + a[0] + ", " + a[1] + "), " + a[2] + ")"
	})

	// --- list and map ---
	// Go is statically typed, so these name the element type they build rather
	// than falling back to []any: a list<int> is emitted as []int, and an []any
	// result would not assign to it.
	regImp("ListJoin", []string{"strings"}, func(a []string) string { return "strings.Join(" + a[0] + ", " + a[1] + ")" })
	regImp("ListIndexOf", []string{"slices"}, func(a []string) string { return "slices.Index(" + a[0] + ", " + a[1] + ")" })
	reg("ListSlice", func(a []string) string { return "(" + a[0] + ")[" + a[1] + ":" + a[2] + "]" })
	regTyped("ListReverse", nil, func(a []string, ts []*ir.Type) string {
		el := goElem(ts[0])
		return "func() []" + el + " { src := " + a[0] + "; out := make([]" + el + ", len(src)); for i, v := range src { out[len(src)-1-i] = v }; return out }()"
	})
	regTyped("ListFilter", nil, func(a []string, ts []*ir.Type) string {
		el := goElem(ts[0])
		return "func() []" + el + " { var out []" + el + "; for _, item := range " + a[0] + " { if " + a[1] + "(item) { out = append(out, item) } }; return out }()"
	})
	regTyped("ListMap", nil, func(a []string, ts []*ir.Type) string {
		// The result element type is the mapper's return type, not the source's.
		out := "any"
		if len(ts) > 1 && ts[1] != nil && ts[1].Sig != nil && ts[1].Sig.Return != nil {
			out = IRTypeToGo(ts[1].Sig.Return)
		}
		return "func() []" + out + " { out := make([]" + out + ", len(" + a[0] + ")); for i, item := range " + a[0] + " { out[i] = " + a[1] + "(item) }; return out }()"
	})
	reg("MapLength", func(a []string) string { return "len(" + a[0] + ")" })
	regTyped("MapKeys", nil, func(a []string, ts []*ir.Type) string {
		k := goKey(ts[0])
		return "func() []" + k + " { ks := make([]" + k + ", 0, len(" + a[0] + ")); for k := range " + a[0] + " { ks = append(ks, k) }; return ks }()"
	})
	regTyped("MapValues", nil, func(a []string, ts []*ir.Type) string {
		v := goVal(ts[0])
		return "func() []" + v + " { vs := make([]" + v + ", 0, len(" + a[0] + ")); for _, v := range " + a[0] + " { vs = append(vs, v) }; return vs }()"
	})
	reg("MapContains", func(a []string) string {
		return "func() bool { _, ok := " + a[0] + "[" + a[1] + "]; return ok }()"
	})
	regTyped("MapGet", nil, func(a []string, ts []*ir.Type) string {
		v := goVal(ts[0])
		return "func() " + v + " { if v, ok := " + a[0] + "[" + a[1] + "]; ok { return v }; return " + a[2] + " }()"
	})

	// --- color ---
	regImp("ColorHex", []string{colorImportPath}, func(a []string) string { return a[0] + ".Hex()" })

	// --- Alert and File ---
	// Effects a generated Go program cannot perform without a UI; print the
	// message and answer with a fixed value, as the interpreter does.
	regImp("Toast", []string{"fmt"}, func(a []string) string {
		return `fmt.Println("[" + ` + a[1] + ` + "] " + ` + a[0] + `)`
	})
	regImp("Info", []string{"fmt"}, func(a []string) string { return `fmt.Println("[info] " + ` + a[0] + `)` })
	regImp("Warn", []string{"fmt"}, func(a []string) string { return `fmt.Println("[warn] " + ` + a[0] + `)` })
	regImp("Error", []string{"fmt"}, func(a []string) string { return `fmt.Println("[error] " + ` + a[0] + `)` })
	reg("Confirm", func(a []string) string { return "true" })
	reg("Pick", func(a []string) string { return `""` })
	reg("PickFolder", func(a []string) string { return `""` })

	// --- intl (locale-aware formatting) ---
	// Each maps to the same-named entry point in the Go i18n runtime; the
	// locale is already the leading argument by the time a call gets here.
	for id, goName := range map[string]string{
		"DefaultLocale": "DefaultLocale", "Translate": "Translate", "Format": "Format",
		"NumberInt": "NumberInt", "NumberFloat": "NumberFloat", "Date": "Date",
		"Time": "Time", "DateTime": "Datetime", "Select": "Select",
		"Plural": "Plural", "SelectOrdinal": "Selectordinal",
	} {
		call := "i18n." + goName
		regImp(id, []string{SnglI18nImportPath}, func(a []string) string {
			return call + "(" + strings.Join(a, ", ") + ")"
		})
	}

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("HtmlFrontend", func(a []string) string { return a[0] })
	reg("HtmlBackend", func(a []string) string { return a[0] })
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
