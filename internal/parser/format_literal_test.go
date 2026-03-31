package parser_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// TestFormatLiteralExprs verifies that Format handles Expr.Literal values
// (the path taken after optimization replaces SNGL expressions with literals).
func TestFormatLiteralExprs(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: "hello"},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: 42},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: 3.14},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: true},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: false},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: nil},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: "#ff0000"},
					},
				},
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: ast.UnitLiteral{Number: "5", Suffix: "s"}},
					},
				},
			},
		},
	}

	result := parser.Format(doc)

	expectations := []string{
		`value="hello"`,
		`value=42`,
		`value=3.14`,
		`value=true`,
		`value=false`,
		`value=null`,
		`value=#ff0000`,
		`value=5s`,
	}
	for _, exp := range expectations {
		if !strings.Contains(result, exp) {
			t.Errorf("expected %q in formatted output:\n%s", exp, result)
		}
	}
}

// TestFormatEventLiteralNull tests the event null path in formatEventValue.
func TestFormatEventLiteralNull(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{
				{
					Component: "button",
					Props: map[string]ast.Expr{
						"text": {Literal: "click"},
					},
					Events: map[string]ast.Expr{
						"click": {}, // no SNGL, no Literal
					},
				},
			},
		},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, "@click={ null }") {
		t.Errorf("expected @click={ null } in output:\n%s", result)
	}
}

// TestFormatStringEscapes tests escapeStringContent paths.
func TestFormatStringEscapes(t *testing.T) {
	doc := &ast.Document{
		App: &ast.App{
			Children: []*ast.VisualNode{
				{
					Component: "text",
					Props: map[string]ast.Expr{
						"value": {Literal: "line1\nline2\ttab\"quoted\""},
					},
				},
			},
		},
	}
	result := parser.Format(doc)
	if !strings.Contains(result, `\n`) || !strings.Contains(result, `\t`) || !strings.Contains(result, `\"`) {
		t.Errorf("expected escaped characters in output:\n%s", result)
	}
}
