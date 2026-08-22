package golang

import (
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
	regImp("ListJoin", []string{"strings"}, func(a []string) string { return "strings.Join(" + a[0] + ", " + a[1] + ")" })
	reg("ListFilter", func(a []string) string {
		return "func() []any { var out []any; for _, item := range " + a[0] + " { if " + a[1] + ".(func(any) any)(item).(bool) { out = append(out, item) } }; return out }()"
	})
	reg("ListMap", func(a []string) string {
		return "func() []any { out := make([]any, len(" + a[0] + ")); for i, item := range " + a[0] + " { out[i] = " + a[1] + ".(func(any) any)(item) }; return out }()"
	})
	reg("MapLength", func(a []string) string { return "len(" + a[0] + ")" })
	reg("MapKeys", func(a []string) string {
		return "func() []any { ks := make([]any, 0, len(" + a[0] + ")); for k := range " + a[0] + " { ks = append(ks, k) }; return ks }()"
	})
	reg("MapValues", func(a []string) string {
		return "func() []any { vs := make([]any, 0, len(" + a[0] + ")); for _, v := range " + a[0] + " { vs = append(vs, v) }; return vs }()"
	})
	reg("MapContains", func(a []string) string {
		return "func() bool { _, ok := " + a[0] + "[" + a[1] + "]; return ok }()"
	})
	reg("MapGet", func(a []string) string {
		return "func() any { if v, ok := " + a[0] + "[" + a[1] + "]; ok { return v }; return " + a[2] + " }()"
	})

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

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("HtmlFrontend", func(a []string) string { return a[0] })
	reg("HtmlBackend", func(a []string) string { return a[0] })
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
