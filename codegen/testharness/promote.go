package testharness

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Promote returns a Document with the named component's body promoted
// into a synthetic `window` so per-platform codegen always deals with
// visual stmts under a window root. Declaration stmts in the component
// body (var/const/func/struct/enum/unit) lift to package level so they
// remain state on Model. Component params become package-level vars
// initialized to their defaults.
//
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
		switch d := s.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.UnitDef:
			stmts = append(stmts, s)
		case *ast.ComponentDecl:
			// Drop the component we're promoting from — its body
			// decls are about to be lifted to root, and keeping the
			// original would re-register them via the pass1
			// component-body walk.
			if d.Name == name {
				continue
			}
			stmts = append(stmts, s)
		}
	}

	// Split the component body: decls lift to package level so they
	// become Model state; everything else (visual nodes, control flow)
	// goes inside the synthetic window.
	var windowBody []ast.Stmt
	for _, s := range comp.Body.Stmts {
		switch s.(type) {
		case *ast.VarDecl, *ast.ConstDecl, *ast.FuncDef,
			*ast.StructDef, *ast.EnumDef, *ast.UnitDef:
			stmts = append(stmts, s)
		default:
			windowBody = append(windowBody, s)
		}
	}

	for _, p := range compParams(comp) {
		stmts = append(stmts, &ast.VarDecl{
			Specs: []ast.VarSpec{{
				Names:   []string{p.Name},
				Default: p.Default,
			}},
		})
	}

	stmts = append(stmts, &ast.VisualNode{
		Pos:    comp.Pos,
		Target: &ast.IdentExpr{Name: "window"},
		Block:  ast.StmtBlock{Stmts: windowBody},
	})

	return &ast.Document{Stmts: stmts}
}

func compParams(comp *ast.ComponentDecl) []ast.Param { return codegen.CompParams(comp) }
