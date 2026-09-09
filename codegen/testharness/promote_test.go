package testharness

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestPromote_extractsBody(t *testing.T) {
	src := `component Counter(start int = 0) node {
    var count = start
    text #lbl(value=string(count))
}

component Other node { text(value="x") }
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Promote(doc, "Counter")
	if out == nil {
		t.Fatal("Promote returned nil for known component")
	}

	var components, varDecls, visualNodes int
	for _, s := range out.Stmts {
		switch s.(type) {
		case *ast.ComponentDecl:
			components++
		case *ast.VarDecl:
			varDecls++
		case *ast.CallStmt, *ast.VisualNode:
			visualNodes++
		}
	}
	if components < 1 {
		t.Errorf("expected component decls preserved, got %d", components)
	}
	if varDecls == 0 {
		t.Errorf("expected promoted var/param decls, got 0")
	}
	if visualNodes == 0 {
		t.Errorf("expected promoted visual nodes, got 0")
	}

	if Promote(doc, "Nope") != nil {
		t.Errorf("unknown component must return nil")
	}
}
