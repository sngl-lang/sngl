package checker

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// finishTreeMarks reads a component's return position as the tree it is a
// member of, which is the whole of what that position says. Naming one of the
// component's own type parameters makes it a wrapper whose family is whatever
// it was handed; naming something that is not a tree is an error rather than a
// children contract — a slot is what declares those.
//
// Naming nothing is an error too. An omitted tree used to mean the default
// family, which was the absence of a check rather than a family; it now means
// no family at all, and inferring which one was meant from the body needs a
// fixpoint over mutually recursive declarations that nothing here has.
func (c *checker) finishTreeMarks(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) {
	if decl == nil || comp == nil {
		return
	}
	named := comp.ChildrenType
	comp.ChildrenType = nil

	if named == nil {
		if comp.Treeless || c.treeOptional(decl, comp) {
			return
		}
		c.error(decl.Pos, "component %s: name the tree it belongs to in the return position, or mark it #[tree.none]",
			comp.Name)
		return
	}
	if comp.Treeless {
		c.error(decl.Pos, "component %s: #[tree.none] says it belongs to no tree, and the return position names %s",
			comp.Name, named)
		return
	}

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
	if named.Kind == ir.TypeTypeParam && slices.ContainsFunc(comp.TypeParams,
		func(tp ir.TypeParam) bool { return tp.Name == named.ParamName }) {
		comp.TreeParam = named.ParamName
		return
	}
	c.error(decl.Pos, "component %s: the return position names the tree a component belongs to, and %s is not one",
		comp.Name, named)
}

// treeOptional reports the declarations an omitted return position is still
// read as "no tree" for, rather than as the missing annotation it now is.
//
// An extension body (`component ui.vbox[platform]`) restates no part of the
// declaration it merges into, so it names no tree either. `output` is the
// other: it parses as a component but the compiler reads it as a build
// directive before any tree question is asked, and it is declared in
// `sngl:builtin`, which cannot import the package the root tree lives in.
func (c *checker) treeOptional(decl *ast.ComponentDecl, comp *ir.Component) bool {
	return decl.Target != nil || strings.Contains(decl.Name, ".") || comp.Builtin.IsDirective()
}

// checkHasWindow reports a program that opens none. The package body renders
// nothing by itself -- it is a slot for the root tree, whose one renderable
// member is a window -- so a program without one has nowhere to draw.
//
// Only the program's own package: a library is imported by one that has a
// window, and a lib package may not declare one at all.
func (c *checker) checkHasWindow() {
	if !c.cfg.IsMain || c.inLibSource() {
		return
	}
	found := len(c.pkg.Windows) > 0
	seen := func(stmts []ir.Stmt) {
		ir.WalkStmts(stmts, func(s ir.Stmt) error {
			if _, ok := s.(*ir.Window); ok {
				found = true
				return ir.SkipDir
			}
			return nil
		})
	}
	seen(c.pkg.Body)
	for _, comp := range c.pkg.Components {
		if found {
			break
		}
		seen(comp.Body)
	}
	if !found {
		// Nothing is missing at a particular place, so the diagnostic goes to
		// the top of the first file rather than to a statement that would
		// suggest the fix belongs there.
		c.error(firstFilePos(c.docs), "a program declares at least one window: the package body renders only what a window holds")
	}
}

// firstFilePos is the top of the package's first file: where a diagnostic
// about the package as a whole goes, since nothing in it is at fault.
func firstFilePos(docs []*ast.Document) ast.Pos {
	for _, d := range docs {
		if name := docFileName(d); name != "" {
			return ast.Pos{File: name, Line: 1, Column: 1}
		}
	}
	return ast.Pos{}
}

// checkTreelessBody holds a tree-less component to containing no member of any
// family. Placing one is unrestricted (a lifetime bracket belongs in a drawing
// as much as in a layout), and this is what keeps that from being a hole: a
// body that renders a `ui` node has joined that family without saying so, and
// would then be placeable in a canvas.
//
// An `if` or a `for` is how the nodes under it got there rather than a node,
// the same reading checkTreeMembership gives them.
func (c *checker) checkTreelessBody(comp *ir.Component) {
	if comp == nil || comp.Tree != nil || comp.AST == nil {
		return
	}
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, st := range stmts {
			switch s := st.(type) {
			case *ir.If:
				walk(s.Body)
				walk(s.Else)
				continue
			case *ir.For:
				walk(s.Body)
				walk(s.Else)
				continue
			}
			ni, ok := st.(*ir.NodeInst)
			if !ok || ni.Component == nil || ni.Component.Tree == nil {
				continue
			}
			at := comp.AST.Pos
			if sp := stmtPos(ni.AST); sp != nil {
				at = *sp
			}
			c.error(at, "component %s names no tree, so it may not contain the %s component %s",
				comp.Name, ni.Component.Tree.Name, ni.Component.Name)
		}
	}
	walk(comp.Body)
}
