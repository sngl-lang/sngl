package parser

import (
	"bytes"
	"encoding/json"
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
	e, err := ParseNativeValue("results", []byte(`[import("go:example.com/p").Item{Name = "a"}]`))
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
	if want := (ast.NativeRef{Path: "go:example.com/p", Name: "Item"}); s.Native == nil || *s.Native != want {
		t.Errorf("Native = %+v, want %+v", s.Native, want)
	}
	if len(s.Fields) != 1 || s.Fields[0].Name != "Name" {
		t.Errorf("fields = %v", s.Fields)
	}
}

// The type ref is unwritable in ordinary source: an import is a declaration
// there and nothing else. Without that a program could claim a declaration the
// compiler would then trust.
func TestOrdinaryParseRejectsTypeRef(t *testing.T) {
	// Both positions reach PrimaryExpr, which is where the production is. An
	// argument does not — NonIdentPrimary spells the alternatives out again —
	// so a case there would fail on a syntax error and prove nothing.
	for _, src := range []string{
		`const x = import("go:example.com/p").Item{Name = "a"}`,
		`const xs = [import("go:example.com/p").Item{}]`,
	} {
		_, err := Parse("t.sngl", []byte(src))
		if err == nil {
			t.Fatalf("Parse(%s) accepted a native type ref", src)
		}
		// The grammar has the production, so the rejection has to come from the
		// gate. A syntax error would mean this passed for the wrong reason.
		if !strings.Contains(err.Error(), "import is not an expression") {
			t.Errorf("Parse(%s) failed with %v, want the gate", src, err)
		}
	}
}

// The gate is the whole of the mode: the same expression the native-value
// parse builds a ref from leaves no ref behind in a document parse.
func TestNativeTypeRefIsModeOnly(t *testing.T) {
	const src = `import("go:example.com/p").Item{Name = "a"}`
	e, err := ParseNativeValue("results", []byte(src))
	if err != nil {
		t.Fatalf("ParseNativeValue: %v", err)
	}
	if s, ok := e.(*ast.StructExpr); !ok || s.Native == nil || s.Native.Name != "Item" {
		t.Fatalf("native-value parse produced %#v, want a ref", e)
	}
	doc, _ := Parse("t.sngl", []byte("const x = "+src))
	blob, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(blob, []byte(`"Native"`)) {
		t.Errorf("document parse left a ref in the AST: %s", blob)
	}
}
