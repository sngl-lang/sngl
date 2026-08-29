package parser_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lspcore"
	. "git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

// stringLiterals lists the spelling of every string literal in a document, in
// walk order.
func stringLiterals(doc *ast.Document) []string {
	var out []string
	lspcore.WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if _, isString := ast.StringStyleOf(lit.Kind); isString {
			out = append(out, lit.Raw)
		}
	})
	return out
}

// A format must not rewrite a string. The formatter used to print a literal's
// decoded content back, so `"a\nb"` came out of `sngl fmt` with a real newline
// in it — a different program that happened to still parse.
func assertLiteralsSurviveFormat(t *testing.T, name, src string) {
	t.Helper()
	doc, err := Parse(name, []byte(src))
	if err != nil {
		return // parse failures are another test's business
	}
	formatted := Format(doc)
	reparsed, err := Parse(name, []byte(formatted))
	if err != nil {
		t.Errorf("formatted output does not re-parse: %v\n%s", err, formatted)
		return
	}
	before, after := stringLiterals(doc), stringLiterals(reparsed)
	if len(before) != len(after) {
		t.Errorf("format changed the literal count: %d before, %d after", len(before), len(after))
		return
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("format rewrote a string literal:\nbefore: %q\nafter:  %q", before[i], after[i])
		}
	}
}

func TestFormatPreservesTestdataLiterals(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			if s.ExpectsError("parse") {
				t.Skip("has ERROR(parse) directive")
			}
			assertLiteralsSurviveFormat(t, s.Filename, s.Source)
		})
	}
}

func TestFormatPreservesDocLiterals(t *testing.T) {
	for s := range testutil.DocSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			assertLiteralsSurviveFormat(t, s.Filename, s.Source)
		})
	}
}
