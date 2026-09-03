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
	"for":       "A loop. Its head says what it does: an iterable is walked (`for var x = xs` declares the element), a bool is a condition, and no head at all runs until the body leaves the loop.",
	"return":    "Returns a value from a function.",
	"break":     "Ends the innermost enclosing loop.",
	"continue":  "Ends this iteration of the innermost enclosing loop and begins the next.",
	"import":    "Imports a module.",
	"slot":      "Declares or populates a named slot — a region of UI the caller supplies.",
	"true":      "Boolean literal.",
	"false":     "Boolean literal.",
	"nil":       "Null literal.",
}
