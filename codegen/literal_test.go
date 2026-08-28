package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// ir.Literal.Raw holds a string's *decoded content* — the lexer builds it from
// a strings.Builder that never receives the delimiters, and every construction
// site in the compiler stores an unquoted value (see the note beside the i18n
// args map: "Raw stores the unquoted value; language codegen applies
// target-language quoting").
//
// IRLiteralString used to strip a leading and trailing quote from it anyway, so
// a string whose own content began and ended with `"` lost them — and when what
// remained was not a valid Go quoted string, strconv.Unquote failed and the
// discarded error left the value empty while ok stayed true. Both are silent:
// the caller is told it read a literal.
func TestIRLiteralStringReturnsTheValueAsWritten(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"plain", "hello"},
		{"empty", ""},
		{"quotes around the whole value", `"quoted"`},
		{"quotes at both ends, not around the whole value", `"a" and "b"`},
		{"a lone quote", `"`},
		{"two quotes", `""`},
		{"leading quote only", `"open`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := IRLiteralString(&ir.Literal{Type: ir.TypString, Raw: tc.raw})
			if !ok {
				t.Fatalf("not recognised as a string literal")
			}
			if got != tc.raw {
				t.Errorf("got %q, want %q", got, tc.raw)
			}
		})
	}
}

func TestIRLiteralStringRejectsNonStrings(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    ir.Expr
	}{
		{"nil", nil},
		{"int literal", &ir.Literal{Type: ir.TypInt, Raw: "3"}},
		{"untyped literal", &ir.Literal{Raw: "x"}},
		{"not a literal", &ir.Ident{Name: "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := IRLiteralString(tc.e); ok {
				t.Errorf("accepted %T as a string literal, returning %q", tc.e, got)
			}
		})
	}
}
