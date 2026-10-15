package javascript

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
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

	// --- sngl:test ---
	// A test body is lowered by testlower.go, which answers these first. A
	// test function emitted into a page as an ordinary function calls the
	// method on the harness's t object instead.
	// The agent's T fails a test through assertTrue, which a helper taking a
	// `Test` reaches here.
	for _, id := range []string{ir.TestAssertID, ir.TestMustID} {
		reg(id, func(a []string) string { return a[0] + ".assertTrue(" + a[1] + ", " + strconv.Quote(a[1]) + ")" })
	}
	// A test body awaits t.wait where it stands (testlower.go); anywhere else
	// the call is in a function that cannot await, so the test fails saying so
	// rather than running on without waiting.
	reg(ir.TestWaitID, func(a []string) string {
		return a[0] + `.fatal("t.wait waits only as a statement of a test's own body on html")`
	})
	for _, id := range []string{ir.TestSetLocaleID, ir.TestSetContextID, ir.TestSnapshotID} {
		method := strings.TrimPrefix(id, "test.")
		reg(id, func(a []string) string { return a[0] + "." + method + "(" + strings.Join(a[1:], ", ") + ")" })
	}

	// --- string ---
	reg("int.parse", func(a []string) string { return "parseInt(" + a[0] + ", " + a[1] + ")" })

	reg("string.length", func(a []string) string { return a[0] + ".length" })
	reg("string.indexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("string.substring", func(a []string) string { return a[0] + ".substring(" + a[1] + ", " + a[2] + ")" })
	reg("string.upper", func(a []string) string { return a[0] + ".toUpperCase()" })
	reg("string.lower", func(a []string) string { return a[0] + ".toLowerCase()" })
	reg("string.trim", func(a []string) string { return a[0] + ".trim()" })
	reg("string.replace", func(a []string) string { return a[0] + ".replaceAll(" + a[1] + ", " + a[2] + ")" })
	reg("string.split", func(a []string) string { return a[0] + ".split(" + a[1] + ")" })

	// --- sngl:time ---
	// ES reads an offsetless date-time as *local* time, and a SNGL datetime
	// literal may be written without one -- so the parse supplies Z, and only
	// where no offset is there: appending it unconditionally makes `...+05:00`
	// a string Date.parse answers NaN for. One evaluation of the operand,
	// because the replacement is a function of what matched.
	utc := func(x string) string {
		return x + `.replace(/(?:Z|[+-]\d\d:?\d\d)?$/, (m) => m || "Z")`
	}
	reg("time.now", func(a []string) string { return "new Date().toISOString()" })
	reg("datetime.add", func(a []string) string {
		return "new Date(Date.parse(" + utc(a[0]) + ") + (" + a[1] + ")).toISOString()"
	})
	reg("time.between", func(a []string) string {
		return "(Date.parse(" + utc(a[1]) + ") - Date.parse(" + utc(a[0]) + "))"
	})

	// --- float math ---
	reg("float.floor", func(a []string) string { return "Math.floor(" + a[0] + ")" })
	reg("float.ceil", func(a []string) string { return "Math.ceil(" + a[0] + ")" })
	reg("float.round", func(a []string) string { return "Math.round(" + a[0] + ")" })
	reg("math.pow", func(a []string) string { return "Math.pow(" + a[0] + ", " + a[1] + ")" })
	reg("math.sqrt", func(a []string) string { return "Math.sqrt(" + a[0] + ")" })
	reg("math.sin", func(a []string) string { return "Math.sin(" + a[0] + ")" })
	reg("math.cos", func(a []string) string { return "Math.cos(" + a[0] + ")" })
	reg("math.tan", func(a []string) string { return "Math.tan(" + a[0] + ")" })
	reg("math.asin", func(a []string) string { return "Math.asin(" + a[0] + ")" })
	reg("math.acos", func(a []string) string { return "Math.acos(" + a[0] + ")" })
	reg("math.atan", func(a []string) string { return "Math.atan(" + a[0] + ")" })
	reg("math.atan2", func(a []string) string { return "Math.atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list and map ---
	// A SNGL map is a JS Map, so these are its methods rather than object keys.
	reg("list.length", func(a []string) string { return a[0] + ".length" })
	reg("list.indexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("list.join", func(a []string) string { return a[0] + ".join(" + a[1] + ")" })
	reg("list.reverse", func(a []string) string { return "[..." + a[0] + "].reverse()" })
	// Array.prototype.slice reads a negative bound as an offset from the end;
	// slice is documented to clamp both bounds to [0, length].
	reg("list.slice", func(a []string) string {
		return "((__l, __a, __b) => { const __n = __l.length; " +
			"const __lo = Math.min(Math.max(__a, 0), __n); " +
			"const __hi = Math.min(Math.max(__b, 0), __n); " +
			"return __l.slice(__lo, Math.max(__lo, __hi)) })(" + a[0] + ", " + a[1] + ", " + a[2] + ")"
	})
	reg("list.filter", func(a []string) string { return a[0] + ".filter(" + a[1] + ")" })
	reg("list.map", func(a []string) string { return a[0] + ".map(" + a[1] + ")" })
	reg("map.length", func(a []string) string { return a[0] + ".size" })
	reg("map.keys", func(a []string) string { return "Array.from(" + a[0] + ".keys())" })
	reg("map.values", func(a []string) string { return "Array.from(" + a[0] + ".values())" })
	reg("map.contains", func(a []string) string { return a[0] + ".has(" + a[1] + ")" })
	reg("map.get", func(a []string) string {
		return "(" + a[0] + ".has(" + a[1] + ") ? " + a[0] + ".get(" + a[1] + ") : " + a[2] + ")"
	})

	// --- numeric ---
	regVariadicMinMax("int.min", "Math.min")
	regVariadicMinMax("int.max", "Math.max")
	reg("int.abs", func(a []string) string { return "Math.abs(" + a[0] + ")" })
	// max(lo, min(x, hi)): with a malformed range lo wins, which is what the
	// declaration documents and its own body computes.
	reg("int.clamp", func(a []string) string {
		return "Math.max(" + a[1] + ", Math.min(" + a[0] + ", " + a[2] + "))"
	})
	regVariadicMinMax("float.min", "Math.min")
	regVariadicMinMax("float.max", "Math.max")
	reg("float.abs", func(a []string) string { return "Math.abs(" + a[0] + ")" })
	reg("float.clamp", func(a []string) string {
		return "Math.max(" + a[1] + ", Math.min(" + a[0] + ", " + a[2] + "))"
	})

	// --- seq (sngl:seq) ---
	// A sequence in a loop head becomes a counting `for` and never reaches
	// here (JsIRContext.ForHead). Everywhere else it is a generator: an
	// iterable `for...of` drives one value at a time, which is what an array
	// here would have thrown away. No conversion is needed in the other
	// direction -- a JS array is already iterable -- so a list reaching an
	// iter<T> position passes through unwrapped.
	seq := func(a, b, step string) string {
		return "(function* (__a, __b, __s) { " +
			"if (__s > 0) { for (let __i = __a; __i < __b; __i += __s) yield __i; } " +
			"else if (__s < 0) { for (let __i = __a; __i > __b; __i += __s) yield __i; } " +
			"})(" + a + ", " + b + ", " + step + ")"
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
		reg(id, func(a []string) string {
			return jsBuiltinMethodFromArgs(qual, a)
		})
	}

	// --- color ---
	// A SNGL color is a {r,g,b,a} object in JS, so hex has to format it. The
	// arrow keeps the operand from being evaluated four times.
	reg("color.hex", func(a []string) string {
		return `(c => "#" + (c.a === 255 ? [c.r, c.g, c.b] : [c.r, c.g, c.b, c.a])` +
			`.map(v => v.toString(16).padStart(2, "0")).join(""))(` + a[0] + ")"
	})

	// --- Alert and File ---
	reg("Alert.toast", func(a []string) string {
		return `(function(){var __d=document.createElement("div");__d.textContent=` + a[0] + `;__d.style.cssText="position:fixed;bottom:16px;left:50%;transform:translateX(-50%);padding:12px 24px;border-radius:8px;color:#fff;z-index:9999;background:#333";document.body.appendChild(__d);setTimeout(function(){__d.remove()},3000)})()`
	})
	reg("Alert.info", func(a []string) string { return "alert(" + a[0] + ")" })
	reg("Alert.warn", func(a []string) string { return `alert("Warning: " + ` + a[0] + ")" })
	reg("Alert.error", func(a []string) string { return `alert("Error: " + ` + a[0] + ")" })
	reg("Alert.confirm", func(a []string) string { return "confirm(" + a[0] + ")" })
	// A file dialog has no synchronous form in a browser; answer as the Go
	// backend does rather than emit something that cannot return a path.
	reg("File.pick", func(a []string) string { return `""` })
	reg("File.pickFolder", func(a []string) string { return `""` })

	reg("list.push", func(a []string) string { return a[0] + ".push(" + a[1] + ")" })
	// splice reads a negative index as an offset from the end; an out-of-range
	// index is documented to leave the list unchanged.
	reg("list.remove", func(a []string) string {
		return "((__l, __i) => { if (__i >= 0 && __i < __l.length) __l.splice(__i, 1) })(" +
			a[0] + ", " + a[1] + ")"
	})
	reg("list.insert", func(a []string) string {
		return "((__l, __i, __x) => { if (__i >= 0 && __i <= __l.length) __l.splice(__i, 0, __x) })(" +
			a[0] + ", " + a[1] + ", " + a[2] + ")"
	})
	reg("list.clear", func(a []string) string { return a[0] + ".length = 0" })

	// --- html placement directives (GitLab #27) ---
	// html.frontend(v)/html.backend(v) are identity directives consumed by the
	// html platform's placement analysis; for any non-html target they emit as
	// just the translated argument (pass-through).
	reg("html.frontend", func(a []string) string { return a[0] })
	reg("html.backend", func(a []string) string { return a[0] })

	// --- intl (locale-aware formatting) ---
	// Each maps to the same-named entry point in the javascript i18n runtime; the
	// locale is already the leading argument by the time a call gets here.
	for id, name := range map[string]string{
		"i18n._date":          "date",
		"i18n._dateTime":      "datetime",
		"i18n._defaultLocale": "defaultLocale",
		"i18n._format":        "format",
		"i18n._numberFloat":   "numberFloat",
		"i18n._numberInt":     "numberInt",
		"i18n._plural":        "plural",
		"i18n._select":        "select",
		"i18n._selectOrdinal": "selectordinal",
		"i18n._time":          "time",
		"i18n._translate":     "translate",
	} {
		call := "i18n." + name
		reg(id, func(a []string) string { return call + "(" + strings.Join(a, ", ") + ")" })
	}
}

// regVariadicMinMax emits Math.min and Math.max with a literal rest written
// out, and any other spread into the call.
func regVariadicMinMax(id, fn string) {
	codegen.RegisterIntrinsic(langJS, id, func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		if parts, ok := codegen.VariadicArgs(args, tr); ok {
			return fn + "(" + strings.Join(parts, ", ") + ")", nil
		}
		return fn + "(" + tr(args[0]) + ", ..." + tr(codegen.VariadicRest(args[1])) + ")", nil
	})
}
