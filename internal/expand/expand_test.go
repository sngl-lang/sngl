package expand_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func parseDoc(t *testing.T, src string) *ast.Document {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func TestExpandPre_NoAttrs_NoOp(t *testing.T) {
	doc := parseDoc(t, `component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", diags)
	}
	if _, ok := doc.Stmts[0].(*ast.ComponentDecl); !ok {
		t.Errorf("expected ComponentDecl, got %T", doc.Stmts[0])
	}
}

func TestExpandPre_UnknownAlias(t *testing.T) {
	doc := parseDoc(t, `#[bogus.thing]
component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for unknown alias, got none")
	}
}

func TestExpandPre_KnownInternal_UnknownName(t *testing.T) {
	doc := parseDoc(t, `import "internal://canvas"
#[canvas.notarealname]
component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for unknown macro name, got none")
	}
}
