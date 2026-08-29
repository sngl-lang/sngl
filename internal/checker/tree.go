package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// finishTreeMarks reads a component's return position as the tree it is a
// member of, which is the whole of what that position says. Naming the default
// tree is the same as naming none, and naming something that is not a tree is
// an error rather than a children contract — a slot is what declares those.
func (c *checker) finishTreeMarks(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) {
	if decl == nil || comp == nil || comp.ChildrenType == nil {
		return
	}
	named := comp.ChildrenType
	comp.ChildrenType = nil

	if sd := treeStruct(named); sd != nil {
		comp.Tree = sd
		pkg.NoteTreeKind(sd.Name)
		return
	}
	if sd, ok := named.Decl.(*ir.StructDef); ok && sd.Builtin == ir.BuiltinTreeDefault {
		return
	}
	c.error(decl.Pos, "component %s: the return position names the tree a component belongs to, and %s is not one",
		comp.Name, named)
}
