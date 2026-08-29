package parser

import "testing"

// A comment belongs to the thing it was written next to. Comments used to be
// merged into the document's statement list alone, so anything written inside
// a block, a struct body, a parameter list or an i18n message came back out at
// the nearest enclosing statement — which for a `// ERROR(check)` directive
// means it stops naming the line the diagnostic lands on.
func TestFormatKeepsCommentsWhereTheyWereWritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"inside a component body", "component a {\n    // about the var\n    var x = 1\n    text(value=\"y\")\n}"},
		{"trailing a statement", "component a {\n    var x = 1 // why\n    text(value=\"y\")\n}"},
		{"on the opening brace", "component a { // why\n    text(value=\"y\")\n}"},
		{"inside an event handler", "component a {\n    button(@click {\n        // why\n        x()\n    })\n}"},
		{"on a struct field", "struct S {\n    // the name\n    name string\n    age int // in years\n}"},
		{"in an enum body", "enum E {\n    // the good one\n    ok\n    err // the other\n}"},
		{"in a parameter list", "component a(\n    // the tag\n    tag string,\n    other string = \"\", // why\n) {}"},
		{"on a mark", "#[some.mark] // why\nvar x = 1"},
		{"among i18n cases", "var m = $\"{n, plural,\n    // none of them\n    =0{none}\n    other{# items}\n}\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFormat(t, tc.src, tc.src)
		})
	}
}

// The blank line above a line belongs to the first statement on it. Two
// statements the source wrote on one line are not separated by it, and giving
// each of them the blank made the formatter grow a line per format.
func TestFormatBlankLineBelongsToTheFirstStatementOnItsLine(t *testing.T) {
	assertFormat(t,
		"var a = 1\n\nfoo bar",
		"var a = 1\n\nfoo\nbar")
}

// Two declarations the source wrote apart stay apart, inside a block as at the
// top level; more than one blank line between them collapses to one.
func TestFormatKeepsOneBlankLine(t *testing.T) {
	assertFormat(t,
		"component a {\n    var x = 1\n\n\n\n    var y = 2\n    text(value=\"z\")\n}",
		"component a {\n    var x = 1\n\n    var y = 2\n    text(value=\"z\")\n}")
}

// Consecutive trailing comments line up one space past the longest of them,
// and a lone one gets a single space.
func TestFormatAlignsTrailingComments(t *testing.T) {
	assertFormat(t,
		"var alpha = 1 // one\nvar b = 2 // two\n\nvar c = 3      // alone",
		"var alpha = 1 // one\nvar b = 2     // two\n\nvar c = 3 // alone")
}
