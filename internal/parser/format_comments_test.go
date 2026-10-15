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
		{"on a struct field", "struct S {\n    // the name\n    name string\n    age  int // in years\n}"},
		{"in an enum body", "enum E {\n    // the good one\n    ok\n    err // the other\n}"},
		{"in a parameter list", "component a(\n    // the tag\n    tag   string,\n    other string = \"\", // why\n) {}"},
		{"on a mark", "#[some.mark] // why\nvar x = 1"},
		{"between two marks", "#[a.one]\n// why the next\n#[a.two]\nvar x = 1"},
		{"trailing a mark that is not the last", "#[a.one] // why\n#[a.two]\nvar x = 1"},
		{"above the first of several marks", "// about all of it\n#[a.one]\n#[a.two]\nvar x = 1"},
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

// A mark's argument list keeps the lines its author gave it, the way a prop
// list does. A capability list runs to twenty names, and joining one gives a
// line no reader can scan -- which is what the formatter used to do, silently,
// to source that had been written to be read.
func TestFormatKeepsAMarkMultiline(t *testing.T) {
	assertFormat(t,
		"#[a.can(\n    one,\n    two,\n)]\nvar x = 1",
		"#[a.can(\n    one,\n    two,\n)]\nvar x = 1")
}

// And a mark written on one line stays on one line: the flag records what the
// author did, and does not turn every mark into a list.
func TestFormatKeepsAMarkOnOneLine(t *testing.T) {
	assertFormat(t, "#[a.can(one, two)]\nvar x = 1", "#[a.can(one, two)]\nvar x = 1")
}

// A comment written inside a multiline mark's argument list stays with the
// argument it follows, and one after the closing `)]` stays with that mark.
//
// Both were relocated: `ast.MacroAttr.Args` is `[]Expr` with nowhere to hold a
// comment, so one among the arguments was swept up as the *next* mark's leading
// comment; and both trailing checks compared against the mark's *opening* line,
// which a multiline mark is not on. The second is why prose explaining one mark
// read as prose explaining the next.
//
// It matters because the target packages carry capability lists of seven to
// twenty names -- exactly where a word per name belongs -- and
// TestLibrarySourceIsFormatted makes whatever the formatter does the mandatory
// spelling.
func TestFormatKeepsCommentsAmongMarkArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"after an argument", "#[a.can(\n    one, // why\n    two,\n)]\nvar x = 1"},
		{"after the last argument", "#[a.can(\n    one,\n    two, // why\n)]\nvar x = 1"},
		{"after a multiline mark", "#[a.can(\n    one,\n)] // why\n#[a.two]\nvar x = 1"},
		{"after the only mark, multiline", "#[a.can(\n    one,\n)] // why\nvar x = 1"},
		// Aligned, because two trailing comments in a run line up one space past
		// the longest -- the formatter's existing rule, and the reason this case
		// is written as it comes out rather than as it was typed.
		{"both at once", "#[a.can(\n    one, // inner\n)]       // outer\n#[a.two]\nvar x = 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFormat(t, tc.src, tc.src)
		})
	}
}

// A declaration's list written across lines lines its types up in a column,
// as gofmt does a struct's fields: per run of items, a blank line ending one.
// An item whose mark is written before it on its line does not join, since
// padding it would line nothing up, and an event writes its parameters in
// parens rather than a type there to align.
func TestFormatAlignsDeclarationColumns(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"component props and events",
			"component c(\n    label string,\n    :checked bool,\n    const size int = 1,\n    @click(e ClickEvent),\n    @done(),\n) {}",
			"component c(\n    label      string,\n    :checked   bool,\n    const size int = 1,\n    @click(e ClickEvent),\n    @done(),\n) {}"},
		{"a blank line ends a run",
			"func f(\n    a int,\n    bb int,\n\n    longer string,\n) {}",
			"func f(\n    a  int,\n    bb int,\n\n    longer string,\n) {}"},
		{"struct fields, broken by a method",
			"struct S {\n    a int\n    bbb int\n    func m() {}\n    cc string\n}",
			"struct S {\n    a   int\n    bbb int\n    func m() {}\n    cc string\n}"},
		{"an inline mark sits out",
			"component c(\n    tag string,\n    #[m.ark] attributes map[string, string],\n) {}",
			"component c(\n    tag string,\n    #[m.ark] attributes map[string, string],\n) {}"},
		{"one line stays as written",
			"func f(a int, bb int) {}",
			"func f(a int, bb int) {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFormat(t, tc.src, tc.want)
		})
	}
}

// An argument on a line of its own names its parameter the way a field of a
// struct literal written across lines does, with spaces; an argument list on
// one line keeps them tight.
func TestFormatSpacesNamedArgumentsAcrossLines(t *testing.T) {
	assertFormat(t,
		"component a {\n    text(\n        value=\"x\",\n        selectable=true,\n    )\n    text(value=\"y\")\n}",
		"component a {\n    text(\n        value = \"x\",\n        selectable = true,\n    )\n    text(value=\"y\")\n}")
}
