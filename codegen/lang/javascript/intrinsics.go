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
	reg("int.parse", func(a []string) string { return "parseInt(" + a[0] + ", " + a[1] + ")" })

	reg("string.length", func(a []string) string { return a[0] + ".length" })
	reg("string.indexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("string.substring", func(a []string) string { return a[0] + ".substring(" + a[1] + ", " + a[2] + ")" })
	reg("string.upper", func(a []string) string { return a[0] + ".toUpperCase()" })
	reg("string.lower", func(a []string) string { return a[0] + ".toLowerCase()" })
	reg("string.trim", func(a []string) string { return a[0] + ".trim()" })
	reg("string.replace", func(a []string) string { return a[0] + ".replaceAll(" + a[1] + ", " + a[2] + ")" })
	reg("string.split", func(a []string) string { return a[0] + ".split(" + a[1] + ")" })

	// --- float math ---
	reg("float.floor", func(a []string) string { return "Math.floor(" + a[0] + ")" })
	reg("float.ceil", func(a []string) string { return "Math.ceil(" + a[0] + ")" })
	reg("float.round", func(a []string) string { return "Math.round(" + a[0] + ")" })
	reg("float.pow", func(a []string) string { return "Math.pow(" + a[0] + ", " + a[1] + ")" })
	reg("float.sqrt", func(a []string) string { return "Math.sqrt(" + a[0] + ")" })
	reg("float.sin", func(a []string) string { return "Math.sin(" + a[0] + ")" })
	reg("float.cos", func(a []string) string { return "Math.cos(" + a[0] + ")" })
	reg("float.tan", func(a []string) string { return "Math.tan(" + a[0] + ")" })
	reg("float.asin", func(a []string) string { return "Math.asin(" + a[0] + ")" })
	reg("float.acos", func(a []string) string { return "Math.acos(" + a[0] + ")" })
	reg("float.atan", func(a []string) string { return "Math.atan(" + a[0] + ")" })
	reg("float.atan2", func(a []string) string { return "Math.atan2(" + a[0] + ", " + a[1] + ")" })

	// --- list and map ---
	// A SNGL map is a JS Map, so these are its methods rather than object keys.
	reg("list.length", func(a []string) string { return a[0] + ".length" })
	reg("list.indexOf", func(a []string) string { return a[0] + ".indexOf(" + a[1] + ")" })
	reg("list.join", func(a []string) string { return a[0] + ".join(" + a[1] + ")" })
	reg("list.reverse", func(a []string) string { return "[..." + a[0] + "].reverse()" })
	reg("list.slice", func(a []string) string { return a[0] + ".slice(" + a[1] + ", " + a[2] + ")" })
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
	reg("int.min", func(a []string) string { return "Math.min(" + a[0] + ", " + a[1] + ")" })
	reg("int.max", func(a []string) string { return "Math.max(" + a[0] + ", " + a[1] + ")" })
	reg("int.abs", func(a []string) string { return "Math.abs(" + a[0] + ")" })
	reg("int.clamp", func(a []string) string {
		return "Math.min(Math.max(" + a[0] + ", " + a[1] + "), " + a[2] + ")"
	})
	reg("float.min", func(a []string) string { return "Math.min(" + a[0] + ", " + a[1] + ")" })
	reg("float.max", func(a []string) string { return "Math.max(" + a[0] + ", " + a[1] + ")" })
	reg("float.abs", func(a []string) string { return "Math.abs(" + a[0] + ")" })
	reg("float.clamp", func(a []string) string {
		return "Math.min(Math.max(" + a[0] + ", " + a[1] + "), " + a[2] + ")"
	})

	reg("i18n.exactly", func(a []string) string { return `("=" + (` + a[0] + "))" })

	// --- color ---
	// A SNGL color is a {r,g,b,a} object in JS, so hex has to format it. The
	// arrow keeps the operand from being evaluated four times.
	reg("color.hex", func(a []string) string {
		return `(c => "#" + (c.a === 255 ? [c.r, c.g, c.b] : [c.r, c.g, c.b, c.a])` +
			`.map(v => v.toString(16).padStart(2, "0")).join(""))(` + a[0] + ")"
	})

	// --- Alert and File ---
	reg("Alert.toast", func(a []string) string {
		return `(function(){var d=document.createElement("div");d.textContent=` + a[0] + `;d.style.cssText="position:fixed;bottom:16px;left:50%;transform:translateX(-50%);padding:12px 24px;border-radius:8px;color:#fff;z-index:9999;background:#333";document.body.appendChild(d);setTimeout(function(){d.remove()},3000)})()`
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
	reg("list.remove", func(a []string) string { return a[0] + ".splice(" + a[1] + ", 1)" })

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
