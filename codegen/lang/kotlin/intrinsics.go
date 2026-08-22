package kotlin

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// langKt is this backend's registry key (matches LanguageIdentifier).
const langKt = "kotlin"

// init registers the Kotlin emitter for the intrinsics this backend implements
// by ID (see codegen.EmitIntrinsicCall). Only import-free intrinsics are
// registered here: the in-place list mutations map to MutableList.add /
// removeAt. Intrinsics whose native form needs an import flow through
// ktBuiltinMethodFromArgs until the emitter interface can declare imports.
func init() {
	// Most emissions below are a Kotlin builtin (String members, MutableList
	// members) or fully qualified (java.lang.Math, auto-imported on the JVM)
	// and declare no imports; the i18n entry points need the runtime class.
	regImp := func(id string, imports []string, fn func(a []string) string) {
		codegen.RegisterIntrinsic(langKt, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
			a := make([]string, len(args))
			for i, e := range args {
				a[i] = tr(e)
			}
			return fn(a), imports
		})
	}
	reg := func(id string, fn func(a []string) string) { regImp(id, nil, fn) }

	// --- string ---
	// toIntOrNull keeps a bad string from throwing; int.parse has no error result.
	reg("IntParse", func(a []string) string { return "(" + a[0] + ".toIntOrNull(" + a[1] + ") ?: 0)" })

	reg("StrLength", func(a []string) string { return a[0] + ".length" })
	reg("StrIndexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("StrSubstring", func(a []string) string { return a[0] + ".substring(" + a[1] + ", " + a[2] + ")" })
	reg("StrUpper", func(a []string) string { return a[0] + ".uppercase()" })
	reg("StrLower", func(a []string) string { return a[0] + ".lowercase()" })
	reg("StrTrim", func(a []string) string { return a[0] + ".trim()" })
	reg("StrReplace", func(a []string) string { return a[0] + ".replace(" + a[1] + ", " + a[2] + ")" })
	reg("StrSplit", func(a []string) string { return a[0] + ".split(" + a[1] + ")" })

	// --- float math --- (java.lang.Math; Floor/Ceil/Round return int)
	reg("MathFloor", func(a []string) string { return "Math.floor(" + a[0] + ").toInt()" })
	reg("MathCeil", func(a []string) string { return "Math.ceil(" + a[0] + ").toInt()" })
	reg("MathRound", func(a []string) string { return "Math.round(" + a[0] + ").toInt()" })
	reg("MathSqrt", func(a []string) string { return "Math.sqrt(" + a[0] + ")" })
	reg("MathPow", func(a []string) string { return "Math.pow(" + a[0] + ", " + a[1] + ")" })
	reg("MathSin", func(a []string) string { return "Math.sin(" + a[0] + ")" })
	reg("MathCos", func(a []string) string { return "Math.cos(" + a[0] + ")" })
	reg("MathTan", func(a []string) string { return "Math.tan(" + a[0] + ")" })
	reg("MathAsin", func(a []string) string { return "Math.asin(" + a[0] + ")" })
	reg("MathAcos", func(a []string) string { return "Math.acos(" + a[0] + ")" })
	reg("MathAtan", func(a []string) string { return "Math.atan(" + a[0] + ")" })
	reg("MathAtan2", func(a []string) string { return "Math.atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list (in-place mutations) ---
	reg("ListPush", func(a []string) string { return a[0] + ".add(" + a[1] + ")" })
	reg("ListRemove", func(a []string) string { return a[0] + ".removeAt(" + a[1] + ")" })

	// --- list and map (non-mutating) ---
	reg("ListLength", func(a []string) string { return a[0] + ".size" })
	reg("ListIndexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("ListJoin", func(a []string) string { return a[0] + ".joinToString(" + a[1] + ")" })
	reg("ListReverse", func(a []string) string { return a[0] + ".reversed()" })
	reg("ListSlice", func(a []string) string { return a[0] + ".subList(" + a[1] + ", " + a[2] + ")" })
	reg("ListFilter", func(a []string) string { return a[0] + ".filter(" + a[1] + ")" })
	reg("ListMap", func(a []string) string { return a[0] + ".map(" + a[1] + ")" })
	reg("MapLength", func(a []string) string { return a[0] + ".size" })
	reg("MapKeys", func(a []string) string { return a[0] + ".keys.toList()" })
	reg("MapValues", func(a []string) string { return a[0] + ".values.toList()" })
	reg("MapContains", func(a []string) string { return a[0] + ".containsKey(" + a[1] + ")" })
	reg("MapGet", func(a []string) string { return a[0] + ".getOrDefault(" + a[1] + ", " + a[2] + ")" })

	// --- numeric ---
	reg("IntMin", func(a []string) string { return "minOf(" + a[0] + ", " + a[1] + ")" })
	reg("IntMax", func(a []string) string { return "maxOf(" + a[0] + ", " + a[1] + ")" })
	reg("IntAbs", func(a []string) string { return "kotlin.math.abs(" + a[0] + ")" })
	reg("IntClamp", func(a []string) string {
		return a[0] + ".coerceIn(" + a[1] + ", " + a[2] + ")"
	})
	reg("FloatMin", func(a []string) string { return "minOf(" + a[0] + ", " + a[1] + ")" })
	reg("FloatMax", func(a []string) string { return "maxOf(" + a[0] + ", " + a[1] + ")" })
	reg("FloatAbs", func(a []string) string { return "kotlin.math.abs(" + a[0] + ")" })
	reg("FloatClamp", func(a []string) string {
		return a[0] + ".coerceIn(" + a[1] + ", " + a[2] + ")"
	})

	// --- color ---
	// The generated Color is a data class of Int channels; let keeps the
	// operand from being evaluated three times.
	reg("ColorHex", func(a []string) string {
		return a[0] + `.let { String.format("#%02x%02x%02x", it.r, it.g, it.b) }`
	})

	// --- Alert and File ---
	// Alert.* is an Android Toast. `context` is in scope because the android
	// backend gates `val context = LocalContext.current` on
	// CommonAnalysis.NeedsToast, and these calls sit inside @Composable
	// handler lambdas that capture it.
	reg("Toast", func(a []string) string {
		return "Toast.makeText(context, " + a[0] + ", Toast.LENGTH_SHORT).show()"
	})
	longToast := func(a []string) string {
		return "Toast.makeText(context, " + a[0] + ", Toast.LENGTH_LONG).show()"
	}
	reg("Info", longToast)
	reg("Warn", longToast)
	reg("Error", longToast)
	reg("Confirm", func(a []string) string { return "true" })
	reg("Pick", func(a []string) string { return `""` })
	reg("PickFolder", func(a []string) string { return `""` })

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("HtmlFrontend", func(a []string) string { return a[0] })
	reg("HtmlBackend", func(a []string) string { return a[0] })

	// --- intl (locale-aware formatting) ---
	// Each maps to the same-named entry point in the kotlin i18n runtime; the
	// locale is already the leading argument by the time a call gets here.
	for id, name := range map[string]string{
		"Date":          "date",
		"DateTime":      "datetime",
		"DefaultLocale": "defaultLocale",
		"Format":        "format",
		"NumberFloat":   "numberFloat",
		"NumberInt":     "numberInt",
		"Plural":        "plural",
		"Select":        "selectStr",
		"SelectOrdinal": "selectordinal",
		"Time":          "time",
		"Translate":     "translate",
	} {
		call := "I18n." + name
		regImp(id, []string{SnglI18nKotlinPackage + ".I18n"}, func(a []string) string {
			return call + "(" + strings.Join(a, ", ") + ")"
		})
	}
}
