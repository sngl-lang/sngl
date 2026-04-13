package html

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// PromoteComponent creates a Document with the named component's fields
// promoted to top-level, suitable for HTML codegen.
func PromoteComponent(doc *ast.Document, name string) *ast.Document {
	comp := codegen.FindComponent(doc, name)
	if comp == nil || len(comp.Body.Stmts) == 0 {
		return nil
	}

	// Start with structs, enums, components from original doc
	var stmts []ast.Stmt
	for _, s := range doc.Stmts {
		switch s.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.ComponentDecl:
			stmts = append(stmts, s)
		}
	}

	// Promote component body stmts (vars, funcs, consts, visual nodes)
	stmts = append(stmts, comp.Body.Stmts...)

	// Add params as VarDecl specs
	params := compParams(comp)
	if len(params) > 0 {
		for _, p := range params {
			stmts = append(stmts, &ast.VarDecl{
				Specs: []ast.VarSpec{{
					Names:   []string{p.Name},
					Default: p.Default,
				}},
			})
		}
	}

	return &ast.Document{Stmts: stmts}
}
