package javascript

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// langJS is this backend's registry key (matches LanguageIdentifier).
const langJS = "js"

// init registers the JavaScript emitter for every intrinsic this backend
// implements. Intrinsics are dispatched by ID (see codegen.EmitIntrinsicCall),
// so neither the call translators nor jsBuiltinMethodFromArgs match method
// names for these — and the dispatch is identical whether a call survives as a
// type-method call or is inlined to a direct intrinsic call.
func init() {
	// reg registers an emitter whose args are rendered positionally; recv is
	// args[0] for a type-method intrinsic. fn receives the already-translated
	// argument strings.
	// All JS emissions below are language builtins (String/Array methods,
	// the global Math object), so none declare imports.
	reg := func(id string, fn func(a []string) string) {
		codegen.RegisterIntrinsic(langJS, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
			a := make([]string, len(args))
			for i, e := range args {
				a[i] = tr(e)
			}
			return fn(a), nil
		})
	}

	// --- string ---
	reg("IntParse", func(a []string) string { return "parseInt(" + a[0] + ", " + a[1] + ")" })

	reg("StrLength", func(a []string) string { return a[0] + ".length" })
	reg("StrIndexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("StrSubstring", func(a []string) string { return a[0] + ".substring(" + a[1] + ", " + a[2] + ")" })
	reg("StrUpper", func(a []string) string { return a[0] + ".toUpperCase()" })
	reg("StrLower", func(a []string) string { return a[0] + ".toLowerCase()" })
	reg("StrTrim", func(a []string) string { return a[0] + ".trim()" })
	reg("StrReplace", func(a []string) string { return a[0] + ".replaceAll(" + a[1] + ", " + a[2] + ")" })
	reg("StrSplit", func(a []string) string { return a[0] + ".split(" + a[1] + ")" })

	// --- float math ---
	reg("MathFloor", func(a []string) string { return "Math.floor(" + a[0] + ")" })
	reg("MathCeil", func(a []string) string { return "Math.ceil(" + a[0] + ")" })
	reg("MathRound", func(a []string) string { return "Math.round(" + a[0] + ")" })
	reg("MathPow", func(a []string) string { return "Math.pow(" + a[0] + ", " + a[1] + ")" })
	reg("MathSqrt", func(a []string) string { return "Math.sqrt(" + a[0] + ")" })
	reg("MathSin", func(a []string) string { return "Math.sin(" + a[0] + ")" })
	reg("MathCos", func(a []string) string { return "Math.cos(" + a[0] + ")" })
	reg("MathTan", func(a []string) string { return "Math.tan(" + a[0] + ")" })
	reg("MathAsin", func(a []string) string { return "Math.asin(" + a[0] + ")" })
	reg("MathAcos", func(a []string) string { return "Math.acos(" + a[0] + ")" })
	reg("MathAtan", func(a []string) string { return "Math.atan(" + a[0] + ")" })
	reg("MathAtan2", func(a []string) string { return "Math.atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list (in-place mutations) ---
	reg("ListPush", func(a []string) string { return a[0] + ".push(" + a[1] + ")" })
	reg("ListRemove", func(a []string) string { return a[0] + ".splice(" + a[1] + ", 1)" })

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("HtmlFrontend", func(a []string) string { return a[0] })
	reg("HtmlBackend", func(a []string) string { return a[0] })

	// --- intl (locale-aware formatting) ---
	// Each maps to the same-named entry point in the javascript i18n runtime; the
	// locale is already the leading argument by the time a call gets here.
	for id, name := range map[string]string{
		"Date":          "date",
		"DateTime":      "datetime",
		"DefaultLocale": "defaultLocale",
		"Format":        "format",
		"NumberFloat":   "numberFloat",
		"NumberInt":     "numberInt",
		"Plural":        "plural",
		"Select":        "select",
		"SelectOrdinal": "selectordinal",
		"Time":          "time",
		"Translate":     "translate",
	} {
		call := "i18n." + name
		reg(id, func(a []string) string { return call + "(" + strings.Join(a, ", ") + ")" })
	}
}
