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
		pkg.NoteTreeKind(sd)
		// A painted shape has nothing to raise an event from. This is drawing's
		// rule rather than one about trees, and it sits here because membership
		// is conferred here -- until a tree can carry rules of its own.
		if ir.IsDrawShapeTree(sd) && len(comp.Events) > 0 {
			c.error(decl.Pos, "component %s: a shape supports no event declarations", comp.Name)
		}
		return
	}
	if sd, ok := named.Decl.(*ir.StructDef); ok && sd.Builtin == ir.BuiltinTreeDefault {
		return
	}
	c.error(decl.Pos, "component %s: the return position names the tree a component belongs to, and %s is not one",
		comp.Name, named)
}
