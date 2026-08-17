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
