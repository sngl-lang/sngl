package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// finishTreeMarks reads a component's return position as the tree it is a
// member of. Naming a tree there is a different statement from naming a
// children type, which the position also still carries, so only the former
// lands on Tree.
func finishTreeMarks(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) {
	if decl == nil || comp == nil || comp.ChildrenType == nil {
		return
	}
	if comp.ChildrenType.Kind != ir.TypeStruct {
		return
	}
	sd, ok := comp.ChildrenType.Decl.(*ir.StructDef)
	if !ok || (!sd.IsTree && sd.Builtin != ir.BuiltinTreeDefault) {
		return
	}
	// The return type said what the component *is*, not what it holds. Naming
	// the default tree is the same as naming none.
	comp.ChildrenType = nil
	if sd.Builtin != ir.BuiltinTreeDefault {
		comp.Tree = sd
		pkg.NoteTreeKind(sd.Name)
	}
}
