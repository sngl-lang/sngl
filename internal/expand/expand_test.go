package expand_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	_ "git.duckfam.us/jonathan/sngl/internal/macros/canvas"
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

func TestExpandPre_CanvasShape_ValidComponent(t *testing.T) {
	doc := parseDoc(t, `import "internal://canvas"
#[canvas.shape]
component rect(x int, y int, w int, h int) {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", diags)
	}
	comp, ok := doc.Stmts[1].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl after expand, got %T", doc.Stmts[1])
	}
	if !comp.IsShape {
		t.Error("expected IsShape == true after canvas.shape macro")
	}
}

func TestExpandPre_CanvasShape_RejectsVar(t *testing.T) {
	doc := parseDoc(t, `import "internal://canvas"
#[canvas.shape]
var bad = 5`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for var decl, got none")
	}
	found := false
	for _, d := range diags {
		if d.Msg == "shape macro requires a component declaration" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'shape macro requires a component declaration', got %v", diags)
	}
}

func TestExpandPre_CanvasShape_RejectsEvent(t *testing.T) {
	doc := parseDoc(t, `import "internal://canvas"
#[canvas.shape]
component badShape(@click) {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for event prop, got none")
	}
	found := false
	for _, d := range diags {
		if d.Msg == "shape components do not support event declarations" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'shape components do not support event declarations', got %v", diags)
	}
}
