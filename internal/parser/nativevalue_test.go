package parser

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

// A native value is one expression, and the shapes an encoder writes all parse
// as one — including the leading minus and the bare list a document-level
// statement has no production for.
func TestParseNativeValueShapes(t *testing.T) {
	cases := []struct {
		src  string
		want string // %T of the expression
	}{
		{`42`, "*ast.LiteralExpr"},
		{`-3.5`, "*ast.UnaryExpr"},
		{`250ms`, "*ast.UnitLiteral"},
		{`null`, "*ast.IdentExpr"},
		{`[1, 2]`, "*ast.ListExpr"},
		{`{"a" = 1}`, "*ast.MapLit"},
		{`Item{Name = "a"}`, "*ast.StructExpr"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			e, err := ParseNativeValue("results", []byte(tc.src))
			if err != nil {
				t.Fatalf("ParseNativeValue(%s): %v", tc.src, err)
			}
			if got := fmt.Sprintf("%T", e); got != tc.want {
				t.Errorf("ParseNativeValue(%s) = %s, want %s", tc.src, got, tc.want)
			}
		})
	}
}

// A value may name the declaration it is of, which is what gives it a type
// where nothing else does.
func TestParseNativeValueTypeRef(t *testing.T) {
	e, err := ParseNativeValue("results", []byte(`[@"go://example.com/p#Item"{Name = "a"}]`))
	if err != nil {
		t.Fatalf("ParseNativeValue: %v", err)
	}
	list, ok := e.(*ast.ListExpr)
	if !ok {
		t.Fatalf("got %T, want a list", e)
	}
	s, ok := list.Elements[0].(*ast.StructExpr)
	if !ok {
		t.Fatalf("element is %T, want a struct literal", list.Elements[0])
	}
	if s.Native != "go://example.com/p#Item" {
		t.Errorf("Native = %q", s.Native)
	}
	if len(s.Fields) != 1 || s.Fields[0].Name != "Name" {
		t.Errorf("fields = %v", s.Fields)
	}
}

// The type ref is unwritable in ordinary source: the token it needs is lexed
// only in native-value mode. Without that a program could claim a declaration
// the compiler would then trust.
func TestOrdinaryParseRejectsTypeRef(t *testing.T) {
	for _, src := range []string{
		`const x = @"go://example.com/p#Item"{Name = "a"}`,
		`component c { text(@"go://example.com/p#Item"{}) }`,
	} {
		if _, err := Parse("t.sngl", []byte(src)); err == nil {
			t.Errorf("Parse(%s) accepted a native type ref", src)
		}
	}
}

// The mode is the lexer's: @ followed by a string is two tokens in ordinary
// source and one in a native value.
func TestNativeTypeTokenIsModeOnly(t *testing.T) {
	const src = `@"go://example.com/p#Item"`
	if got := kinds(Tokenize(src)); strings.Contains(got, "NATIVE_TYPE") {
		t.Errorf("ordinary lexing produced a NATIVE_TYPE token: %s", got)
	}
	if got := kinds(TokenizeNativeValue(src)); !strings.Contains(got, "NATIVE_TYPE") {
		t.Errorf("native-value lexing produced no NATIVE_TYPE token: %s", got)
	}
}

// kinds names the tokens a scan produced, for an assertion about which ones
// a mode can emit at all.
func kinds(tokens []Token, _ []string) string {
	var names []string
	for _, tok := range tokens {
		if tok.Type == NATIVE_TYPE {
			names = append(names, "NATIVE_TYPE")
			continue
		}
		names = append(names, fmt.Sprintf("%#x", byte(tok.Type)))
	}
	return strings.Join(names, " ")
}
