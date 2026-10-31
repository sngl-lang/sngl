package parser

import (
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
)

// A literal's spelling is the formatter's to reprint. Decoding it in the lexer
// meant `"a\nb"` came back with a raw newline in it: still a program, a
// different one, and one whose next format added a blank line where the
// newline had ended the logical line.
func TestFormatPreservesStringEscapes(t *testing.T) {
	for _, src := range []string{
		`const A = "a\nb"`,
		`const A = "x\ty"`,
		`const A = "q\"r"`,
		`const A = "back\\slash"`,
		`const A = "nul\0end"`,
		`const A = "hex\x41end"`,
		`const A = "brace\{not interp\}"`,
		`const A = "carriage\rreturn"`,
		"const A = `raw \\n stays`",
		`const A = """triple \t tab"""`,
		`const A = "before {b} after\nnewline"`,
	} {
		t.Run(src, func(t *testing.T) {
			got := strings.TrimSpace(roundTrip(t, src))
			if got != src {
				t.Errorf("format changed the literal:\ngot:  %s\nwant: %s", got, src)
			}
		})
	}
}

// The lexer's spelling and ast.UnescapeString are one contract read from two
// sides; nothing else forces them to agree.
func TestStringLiteralValues(t *testing.T) {
	cases := map[string]string{
		`"a\nb"`:        "a\nb",
		`"x\ty"`:        "x\ty",
		`"q\"r"`:        `q"r`,
		`"back\\slash"`: `back\slash`,
		`"hex\x41end"`:  "hexAend",
		`"nul\0end"`:    "nul\x00end",
		`"brace\{x\}"`:  "brace{x}",
		"`raw \\n`":     `raw \n`,
	}
	for src, want := range cases {
		t.Run(src, func(t *testing.T) {
			doc, err := Parse("test.sngl", []byte("const A = "+src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			cd := doc.Stmts[0].(*ast.ConstDecl)
			lit, ok := cd.Specs[0].Default.(*ast.LiteralExpr)
			if !ok {
				t.Fatalf("value is %T, want *ast.LiteralExpr", cd.Specs[0].Default)
			}
			got, isString := lit.StringValue()
			if !isString {
				t.Fatalf("StringValue reported a non-string for %s", src)
			}
			if got != want {
				t.Errorf("StringValue() = %q, want %q", got, want)
			}
		})
	}
}

// EscapeString is UnescapeString's inverse, and ir.Convert leans on it to spell
// a value the checker decoded.
func TestEscapeStringRoundTrip(t *testing.T) {
	for _, v := range []string{
		"", "plain", "a\nb", "x\ty", `q"r`, `back\slash`, "nul\x00end",
		"brace{x}", "carriage\rreturn", "unicode ✓ é",
	} {
		for _, style := range []ast.StringStyle{ast.StyleDouble, ast.StyleTriple} {
			if got := ast.UnescapeString(ast.EscapeString(v, style), style); got != v {
				t.Errorf("round trip of %q in %v = %q", v, style, got)
			}
		}
	}
}

// A field written with arguments is a FormatExpr, reprinted as it was written:
// the value, then its format's arguments, named ones and nested strings
// included, on the line the string is on.
func TestFormatInterpolationArguments(t *testing.T) {
	for _, src := range []string{
		`const A = "{n, 16}"`,
		`const A = "a {n, base=16, width=4, pad="0"} b"`,
		`const A = """{r, places=2}"""`,
		`const A = "{p.q(1), sep=", "}"`,
	} {
		t.Run(src, func(t *testing.T) {
			got := strings.TrimSpace(roundTrip(t, src))
			if got != src {
				t.Errorf("format changed the field:\ngot:  %s\nwant: %s", got, src)
			}
		})
	}
	doc, err := Parse("t.sngl", []byte(`const A = "{n, 16, width=4}"`))
	if err != nil {
		t.Fatal(err)
	}
	interp := doc.Stmts[0].(*ast.ConstDecl).Specs[0].Default.(*ast.InterpolationExpr)
	f, ok := interp.Parts[0].(*ast.FormatExpr)
	if !ok {
		t.Fatalf("field is %T, want *ast.FormatExpr", interp.Parts[0])
	}
	if len(f.Args.Args) != 2 || f.Args.Args[1].(ast.Arg).Name != "width" {
		t.Errorf("args = %#v, want 16 and width=4", f.Args.Args)
	}
}
