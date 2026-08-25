package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// applyTreeMarks copies #[tree.kind]/#[tree.children] from a component
// declaration onto the IR component, and records the kinds on the declaring
// package. Both registration paths — lib source and pass1 — call it, so a mark
// means the same thing wherever the component was written.
//
// A member with no children type of its own hosts its own kind: a shape
// contains shapes without saying so. A declared children type says what it
// accepts instead, which is how a member of one tree hosts another.
func applyTreeMarks(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) {
	if decl == nil || comp == nil {
		return
	}
	comp.TreeKind = decl.Tree.Kind
	comp.ChildKind = decl.Tree.Children
	if comp.ChildKind == "" && comp.TreeKind != "" && decl.ChildrenType == nil {
		comp.ChildKind = comp.TreeKind
	}
	// A tree mark is the whole of what a node says about its children, so a
	// marked component needs no children type in source; without one the
	// arity check would read it as accepting none.
	if comp.ChildKind != "" && comp.ChildrenType == nil {
		comp.ChildrenType = ir.ListOf(&ir.Type{Kind: ir.TypeComponent})
	}
	pkg.NoteTreeKind(comp.TreeKind)
	pkg.NoteTreeKind(comp.ChildKind)
}
