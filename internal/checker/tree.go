package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// finishTreeMarks derives what the tree marks imply and records the kinds on
// the declaring package. The marks themselves wrote TreeKind and ChildKind
// when they were applied; this runs once afterwards, so a component that
// carries both marks is read as one node rather than twice.
//
// A member with no children type of its own hosts its own kind: a shape
// contains shapes without saying so. A declared children type says what it
// accepts instead, which is how a member of one tree hosts another.
func finishTreeMarks(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) {
	if decl == nil || comp == nil {
		return
	}
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
