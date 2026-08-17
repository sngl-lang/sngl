package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
)

// FindComponent finds a ComponentDecl by name in the document.
// Kept for backward compatibility.
func FindComponent(doc *ast.Document, name string) *ast.ComponentDecl {
	for _, s := range doc.Stmts {
		if comp, ok := s.(*ast.ComponentDecl); ok && comp.Name == name {
			return comp
		}
	}
	return nil
}
