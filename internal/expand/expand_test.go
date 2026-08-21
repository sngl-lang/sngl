package expand_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	_ "git.duckfam.us/jonathan/sngl/internal/macros/draw"
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
	doc := parseDoc(t, `import "sngl://draw"
#[canvas.notarealname]
component foo() {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for unknown macro name, got none")
	}
}

func TestExpandPre_CanvasShape_ValidComponent(t *testing.T) {
	doc := parseDoc(t, `import "sngl://draw"
#[draw.shape]
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
	doc := parseDoc(t, `import "sngl://draw"
#[draw.shape]
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
	doc := parseDoc(t, `import "sngl://draw"
#[draw.shape]
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

func TestExpandPre_CanvasShape_RewritesListComponent(t *testing.T) {
	doc := parseDoc(t, `import "sngl://draw"
#[draw.shape]
component myShape(x int) list<component> {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	comp := doc.Stmts[1].(*ast.ComponentDecl)
	named := comp.ChildrenType.(*ast.NamedType)
	inner := named.TypeArgs[0].(*ast.NamedType)
	if inner.Name != "shape" {
		t.Fatalf("expected list<shape>, got list<%s>", inner.Name)
	}
}

func TestExpandPre_CanvasShape_RejectsListOtherType(t *testing.T) {
	doc := parseDoc(t, `import "sngl://draw"
#[draw.shape]
component myShape(x int) list<string> {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) == 0 {
		t.Fatal("expected error for list<string> on shape")
	}
}

func TestExpandPre_CanvasShape_SetsChildrenType(t *testing.T) {
	doc := parseDoc(t, `import "sngl://draw"
#[draw.shape]
component myShape(x int) {}`)
	diags := expand.ExpandPre([]*ast.Document{doc})
	if len(diags) != 0 {
		t.Fatalf("unexpected diags: %v", diags)
	}
	comp, ok := doc.Stmts[1].(*ast.ComponentDecl)
	if !ok {
		t.Fatalf("expected ComponentDecl at index 1, got %T", doc.Stmts[1])
	}
	if comp.ChildrenType == nil {
		t.Fatal("expected ChildrenType to be set to list<shape>")
	}
	named, ok := comp.ChildrenType.(*ast.NamedType)
	if !ok || named.Name != "list" {
		t.Fatalf("expected list<shape>, got %v", comp.ChildrenType)
	}
	if len(named.TypeArgs) == 0 {
		t.Fatal("expected type arg")
	}
	inner, ok := named.TypeArgs[0].(*ast.NamedType)
	if !ok || inner.Name != "shape" {
		t.Fatalf("expected shape type arg, got %v", named.TypeArgs[0])
	}
}
