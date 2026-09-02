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
	reg("int.parse", func(a []string) string { return "(" + a[0] + ".toIntOrNull(" + a[1] + ") ?: 0)" })

	reg("string.length", func(a []string) string { return a[0] + ".length" })
	reg("string.indexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("string.substring", func(a []string) string { return a[0] + ".substring(" + a[1] + ", " + a[2] + ")" })
	reg("string.upper", func(a []string) string { return a[0] + ".uppercase()" })
	reg("string.lower", func(a []string) string { return a[0] + ".lowercase()" })
	reg("string.trim", func(a []string) string { return a[0] + ".trim()" })
	reg("string.replace", func(a []string) string { return a[0] + ".replace(" + a[1] + ", " + a[2] + ")" })
	// Kotlin's split answers `["", "a", "b", "c", ""]` for an empty separator
	// where Go and JavaScript both answer `["a", "b", "c"]`. A calculator
	// splitting its entry into characters got two blank cells it never asked
	// for, one of them shifting the whole readout along. The helper is the
	// one meaning, on every target.
	reg("string.split", func(a []string) string { return SplitFn + "(" + a[0] + ", " + a[1] + ")" })

	// --- float math --- (java.lang.Math)
	reg("float.floor", func(a []string) string { return "Math.floor(" + a[0] + ")" })
	reg("float.ceil", func(a []string) string { return "Math.ceil(" + a[0] + ")" })
	// Math.round(Double) returns Long; float is Double.
	reg("float.round", func(a []string) string { return "Math.round(" + a[0] + ").toDouble()" })
	reg("float.sqrt", func(a []string) string { return "Math.sqrt(" + a[0] + ")" })
	reg("float.pow", func(a []string) string { return "Math.pow(" + a[0] + ", " + a[1] + ")" })
	reg("float.sin", func(a []string) string { return "Math.sin(" + a[0] + ")" })
	reg("float.cos", func(a []string) string { return "Math.cos(" + a[0] + ")" })
	reg("float.tan", func(a []string) string { return "Math.tan(" + a[0] + ")" })
	reg("float.asin", func(a []string) string { return "Math.asin(" + a[0] + ")" })
	reg("float.acos", func(a []string) string { return "Math.acos(" + a[0] + ")" })
	reg("float.atan", func(a []string) string { return "Math.atan(" + a[0] + ")" })
	reg("float.atan2", func(a []string) string { return "Math.atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list (in-place mutations) ---
	reg("list.push", func(a []string) string { return a[0] + ".add(" + a[1] + ")" })
	// removeAt throws out of range; an out-of-range index is documented to
	// leave the list unchanged.
	reg("list.remove", func(a []string) string {
		return "run { val __l = " + a[0] + "; val __i = " + a[1] + "; " +
			"if (__i >= 0 && __i < __l.size) __l.removeAt(__i) }"
	})

	// --- list and map (non-mutating) ---
	reg("list.length", func(a []string) string { return a[0] + ".size" })
	reg("list.indexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("list.join", func(a []string) string { return a[0] + ".joinToString(" + a[1] + ")" })
	reg("list.reverse", func(a []string) string { return a[0] + ".reversed()" })
	// subList returns a live view of the receiver and throws out of range;
	// slice is documented to copy and to clamp both bounds to [0, length].
	reg("list.slice", func(a []string) string {
		return "run { val __l = " + a[0] + "; val __n = __l.size; " +
			"val __lo = (" + a[1] + ").coerceIn(0, __n); " +
			"val __hi = (" + a[2] + ").coerceIn(0, __n); " +
			"__l.subList(__lo, maxOf(__lo, __hi)).toList() }"
	})
	reg("list.filter", func(a []string) string { return a[0] + ".filter(" + a[1] + ")" })
	reg("list.map", func(a []string) string { return a[0] + ".map(" + a[1] + ")" })
	reg("map.length", func(a []string) string { return a[0] + ".size" })
	reg("map.keys", func(a []string) string { return a[0] + ".keys.toList()" })
	reg("map.values", func(a []string) string { return a[0] + ".values.toList()" })
	reg("map.contains", func(a []string) string { return a[0] + ".containsKey(" + a[1] + ")" })
	reg("map.get", func(a []string) string { return a[0] + ".getOrDefault(" + a[1] + ", " + a[2] + ")" })

	// --- numeric ---
	reg("int.min", func(a []string) string { return "minOf(" + a[0] + ", " + a[1] + ")" })
	reg("int.max", func(a []string) string { return "maxOf(" + a[0] + ", " + a[1] + ")" })
	reg("int.abs", func(a []string) string { return "kotlin.math.abs(" + a[0] + ")" })
	// max(lo, min(x, hi)): with a malformed range lo wins, which is what the
	// declaration documents and its own body computes.
	// Not coerceIn, which throws when lo > hi rather than answering lo.
	reg("int.clamp", func(a []string) string {
		return "maxOf(" + a[1] + ", minOf(" + a[0] + ", " + a[2] + "))"
	})
	reg("float.min", func(a []string) string { return "minOf(" + a[0] + ", " + a[1] + ")" })
	reg("float.max", func(a []string) string { return "maxOf(" + a[0] + ", " + a[1] + ")" })
	reg("float.abs", func(a []string) string { return "kotlin.math.abs(" + a[0] + ")" })
	reg("float.clamp", func(a []string) string {
		return "maxOf(" + a[1] + ", minOf(" + a[0] + ", " + a[2] + "))"
	})

	// --- seq (sngl:seq) ---
	// Only reached where the numbers themselves are wanted: a sequence in a
	// loop head becomes a range loop (KtIRContext.ForHead). A negative step
	// counts down, and `downTo __b + 1` keeps the end bound exclusive as the
	// declaration says; a `by` of 0 yields nothing rather than spinning.
	seq := func(a, b, step string) string {
		return "run { val __a = " + a + "; val __b = " + b + "; val __s = " + step + "; " +
			"if (__s > 0) (__a until __b step __s).toList() " +
			"else if (__s < 0) ((__a downTo __b + 1) step -__s).toList() " +
			"else emptyList() }"
	}
	reg("seq.count", func(a []string) string { return seq("0", a[0], "1") })
	reg("seq.range", func(a []string) string { return seq(a[0], a[1], "1") })
	reg("seq.step", func(a []string) string { return seq(a[0], a[1], a[2]) })

	reg("i18n.exactly", func(a []string) string { return `("=" + (` + a[0] + "))" })

	// --- i18n entry points ---
	// Dispatched by id: a package function carries no receiver, so the
	// qualified name these were matched by is not there after lowering.
	for _, name := range []string{"defaultLocale", "tr", "trInline", "format", "numberInt", "numberFloat", "date", "time", "datetime", "select", "plural", "selectordinal"} {
		id := "i18n." + name
		qual := id
		regImp(id, []string{SnglI18nKotlinPackage + ".I18n"}, func(a []string) string {
			return kotlinBuiltinMethodFromArgs(qual, a)
		})
	}

	// --- color ---
	// The generated Color is a data class of Int channels; let keeps the
	// operand from being evaluated three times.
	// The operand is the Color data class the android platform declares, not
	// a String — IRTypeToKt maps a color struct to String for an *annotation*
	// while values emit as Color(r=…, g=…, b=…, a=…), which is a discrepancy
	// of its own and the reason this looked like a String.
	reg("color.hex", func(a []string) string {
		return a[0] + `.let { if (it.a == 255) String.format("#%02x%02x%02x", it.r, it.g, it.b) ` +
			`else String.format("#%02x%02x%02x%02x", it.r, it.g, it.b, it.a) }`
	})

	// --- Alert and File ---
	// Alert.* is an Android Toast. `context` is in scope because the android
	// backend gates `val context = LocalContext.current` on
	// CommonAnalysis.NeedsToast, and these calls sit inside @Composable
	// handler lambdas that capture it.
	reg("Alert.toast", func(a []string) string {
		return "Toast.makeText(context, " + a[0] + ", Toast.LENGTH_SHORT).show()"
	})
	longToast := func(a []string) string {
		return "Toast.makeText(context, " + a[0] + ", Toast.LENGTH_LONG).show()"
	}
	reg("Alert.info", longToast)
	reg("Alert.warn", longToast)
	reg("Alert.error", longToast)
	reg("Alert.confirm", func(a []string) string { return "true" })
	reg("File.pick", func(a []string) string { return `""` })
	reg("File.pickFolder", func(a []string) string { return `""` })

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("html.frontend", func(a []string) string { return a[0] })
	reg("html.backend", func(a []string) string { return a[0] })

	// --- intl (locale-aware formatting) ---
	// Each maps to the same-named entry point in the kotlin i18n runtime; the
	// locale is already the leading argument by the time a call gets here.
	for id, name := range map[string]string{
		"i18n._date":          "date",
		"i18n._dateTime":      "datetime",
		"i18n._defaultLocale": "defaultLocale",
		"i18n._format":        "format",
		"i18n._numberFloat":   "numberFloat",
		"i18n._numberInt":     "numberInt",
		"i18n._plural":        "plural",
		"i18n._select":        "selectStr",
		"i18n._selectOrdinal": "selectordinal",
		"i18n._time":          "time",
		"i18n._translate":     "translate",
	} {
		call := "I18n." + name
		regImp(id, []string{SnglI18nKotlinPackage + ".I18n"}, func(a []string) string {
			return call + "(" + strings.Join(a, ", ") + ")"
		})
	}
}
