package lspcore

// keywordDocs maps SNGL keywords to a one-line description shown on hover.
// Keep entries brief (one sentence).
var keywordDocs = map[string]string{
	"var":       "Declares a mutable binding.",
	"const":     "Declares an immutable binding.",
	"func":      "Declares a function.",
	"component": "Declares a UI component.",
	"struct":    "Declares a record type.",
	"enum":      "Declares an enumeration type.",
	"unit":      "Declares a unit type with named suffixes (e.g. px, em).",
	"window":    "Declares a top-level window — the entry point of a UI.",
	"if":        "Conditional statement.",
	"else":      "Alternative branch of an if statement.",
	"for":       "Iteration over a list, map, or iterator. `for var x = xs` declares the element; without `var` the loop binds nothing.",
	"return":    "Returns a value from a function.",
	"import":    "Imports a module.",
	"slot":      "Declares or populates a named slot — a region of UI the caller supplies.",
	"true":      "Boolean literal.",
	"false":     "Boolean literal.",
	"nil":       "Null literal.",
}
