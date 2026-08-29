package parser

import (
	"regexp"
	"strings"
)

// symbolDisplay maps parser-generator symbol names to their source-form
// equivalents for user-facing error messages. Symbols not present here
// keep their original name in the output.
var symbolDisplay = map[string]string{
	"amp":          "&",
	"assign":       "=",
	"at":           "@",
	"bang":         "!",
	"bangbang":     "!!",
	"colon":        ":",
	"comma":        ",",
	"dot":          ".",
	"ellipsis":     "...",
	"eq":           "==",
	"fat_arrow":    "=>",
	"gt":           ">",
	"gte":          ">=",
	"lbrace":       "{",
	"lbracket":     "[",
	"land":         "&&",
	"lor":          "||",
	"lparen":       "(",
	"lt":           "<",
	"lte":          "<=",
	"minus":        "-",
	"minus_assign": "-=",
	"minus_minus":  "--",
	"neq":          "!=",
	"pct_assign":   "%=",
	"percent":      "%",
	"plus":         "+",
	"plus_assign":  "+=",
	"plus_plus":    "++",
	"question":     "?",
	"rbrace":       "}",
	"rbracket":     "]",
	"rparen":       ")",
	"semi":         ";",
	"slash":        "/",
	"slash_assign": "/=",
	"slashdash":    "//",
	"star":         "*",
	"star_assign":  "*=",
	"kw_component": "component",
	"kw_const":     "const",
	"kw_else":      "else",
	"kw_enum":      "enum",
	"kw_for":       "for",
	"kw_func":      "func",
	"kw_if":        "if",
	"kw_import":    "import",
	"kw_return":    "return",
	"kw_struct":    "struct",
	"kw_unit":      "unit",
	"kw_var":       "var",
	"ident":        "identifier",
	"int_lit":      "number",
	"float_lit":    "number",
	"str_full":     "string",
	"raw_str":      "string",
	"color":        "color literal",
	"unit_lit":     "measurement literal",
	"elem_ref":     "#identifier",
}

var (
	// "\xNN" [symbol]: expected [s1 s2 ...]
	zparserMsg = regexp.MustCompile(`^"(?:\\x[0-9a-f]{2}|[^"]*)" \[(\w+)\]: expected (.+)$`)
	// individual symbol token inside the expected list
	bracketed = regexp.MustCompile(`\[([\w_ ]+)\]`)
)

// displayFor returns the source-form rendering of a symbol name, or the
// original name when no mapping exists.
func displayFor(name string) string {
	if d, ok := symbolDisplay[name]; ok {
		return d
	}
	return name
}

// prettifyParseError rewrites an egg-generated parse error message into a
// form that uses source-syntax tokens. Returns the input unchanged when
// the message doesn't match the expected shape.
func prettifyParseError(msg string) string {
	m := zparserMsg.FindStringSubmatch(msg)
	if m == nil {
		return msg
	}
	got := displayFor(m[1])
	expected := prettifyExpectedList(m[2])
	return "unexpected " + quoteToken(got) + ", expected " + expected
}

// prettifyExpectedList rewrites the "expected ..." tail into a quoted
// comma-separated source-syntax list when it's a single bracket group.
func prettifyExpectedList(s string) string {
	s = strings.TrimSpace(s)
	bm := bracketed.FindStringSubmatch(s)
	if bm == nil {
		return s
	}
	parts := strings.Fields(bm[1])
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, quoteToken(displayFor(p)))
	}
	return strings.Join(out, ", ")
}

// quoteToken wraps a single token in double quotes unless it's already
// a descriptive phrase like "identifier" or "number".
func quoteToken(t string) string {
	switch t {
	case "identifier", "number", "string", "color literal", "measurement literal", "#identifier":
		return t
	}
	return `"` + t + `"`
}
