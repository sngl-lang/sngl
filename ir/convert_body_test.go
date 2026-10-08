package ir

import (
	"testing"

	"duckfam.us/sngl/ast"
)

// The package's own body has to survive Convert, or `sngl dump` and `sngl fmt`
// would silently drop whatever a program writes at its top level. Nothing
// fills Package.Body yet, so this is what says the field is wired through
// rather than merely declared.
func TestConvertRendersPackageBody(t *testing.T) {
	pkg := &Package{
		Body: []Stmt{&NodeInst{Name: "vbox", Children: []Stmt{
			&NodeInst{Name: "text"},
		}}},
	}
	doc := Convert(pkg)
	if doc == nil {
		t.Fatal("Convert returned nil")
	}
	var found *ast.VisualNode
	for _, s := range doc.Stmts {
		if vn, ok := s.(*ast.VisualNode); ok {
			if id, ok := vn.Target.(*ast.IdentExpr); ok && id.Name == "vbox" {
				found = vn
			}
		}
	}
	if found == nil {
		t.Fatalf("Convert dropped the package body: no top-level vbox in %d stmts", len(doc.Stmts))
	}
	if len(found.Block.Stmts) != 1 {
		t.Errorf("vbox has %d children; want the nested text", len(found.Block.Stmts))
	}
}
