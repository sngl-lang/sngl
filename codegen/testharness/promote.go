package testharness

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Promote returns a Document with the named component's body promoted to
// top level, plus all component/struct/enum decls from the original.
// Returns nil when the component does not exist or has an empty body.
//
// Used by per-platform test runners to render a single component in
// isolation.
func Promote(doc *ast.Document, name string) *ast.Document {
	comp := codegen.FindComponent(doc, name)
	if comp == nil || len(comp.Body.Stmts) == 0 {
		return nil
	}

	var stmts []ast.Stmt
	for _, s := range doc.Stmts {
		switch s.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.ComponentDecl:
			stmts = append(stmts, s)
		}
	}

	stmts = append(stmts, comp.Body.Stmts...)

	for _, p := range compParams(comp) {
		stmts = append(stmts, &ast.VarDecl{
			Specs: []ast.VarSpec{{
				Names:   []string{p.Name},
				Default: p.Default,
			}},
		})
	}

	return &ast.Document{Stmts: stmts}
}

func compParams(comp *ast.ComponentDecl) []ast.Param { return codegen.CompParams(comp) }
