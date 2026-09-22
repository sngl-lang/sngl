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
// Naming nothing hands the question to inferComponentTrees, which reads the
// answer off the body once every body has been read. Belonging to no family is
// a claim the mark makes, and silence is no longer how it is spelled.
func (c *checker) finishTreeMarks(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) {
	if decl == nil || comp == nil {
		return
	}
	named := comp.ChildrenType
	comp.ChildrenType = nil

	// Where a sngl:x/gen mark may be written, asked here for the reason the
	// shape rule below is: the mark applies while the declaration is still
	// registering, and the return position it has to be measured against is
	// read at this point.
	//
	// A build reads the capabilities off the two nodes its target pair was
	// selected by, so one written anywhere else resolves and is asked nothing
	// -- which is the failure the mark table exists to prevent.
	if comp.Gen != nil && !ir.IsBuildTargetTree(treeStruct(named)) {
		c.error(decl.Pos, "component %s: #[gen] belongs on a build-target node, whose return position is build.language or build.platform", comp.Name)
	}

	if named == nil {
		if comp.Treeless || c.treeOptional(decl, comp) {
			return
		}
		if c.inLibSource() {
			// A library's declarations are a published contract, and only the
			// target tiers have their bodies checked at all -- so there is
			// nothing here to read an answer off, and every tier says it.
			c.error(decl.Pos, "component %s: name the tree it belongs to in the return position, or mark it #[tree.none]",
				comp.Name)
			return
		}
		c.inferTrees = append(c.inferTrees, comp)
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
		//
		// A platform primitive is exempt, and in the opposite direction: its
		// event is not the shape raising anything, it is the target calling in
		// with the drawing context, which is how an override says what to
		// paint. Only an #[intrinsic] declaration can be one.
		if ir.IsDrawShapeTree(sd) && len(comp.Events) > 0 && comp.Intrinsic == "" {
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

// treeTransparent reports the blocks a statement carries the tree question
// into, and whether it is one of the constructs that is *how* the nodes under
// it got there rather than a node in its own right.
//
// Four of them: an `if` and a `for`, which say when and how many; an
// `ir.ErrorBoundary`, which says what happens when one raises; and an
// `ir.ContextProvider`, which sets a value for what is under it. None puts
// anything in the tree itself, so every question asked of a block is asked of
// theirs.
//
// `all` is every block; `binds` is the ones that may *supply* a family rather
// than merely be held to one. They differ by a boundary's fallback alone: it
// stands where the content stood and is held to the content's answer, so a
// caller asking "what family is this?" must not read it. The boundary's own
// check takes the fallback as a last resort once the content has given nothing
// (checkVisualNodeIR), which is where that rule belongs.
func treeTransparent(st ir.Stmt) (all, binds [][]ir.Stmt, ok bool) {
	switch s := st.(type) {
	case *ir.If:
		b := [][]ir.Stmt{s.Body, s.Else}
		return b, b, true
	case *ir.For:
		b := [][]ir.Stmt{s.Body, s.Else}
		return b, b, true
	case *ir.ErrorBoundary:
		return [][]ir.Stmt{s.Children, s.Failed}, [][]ir.Stmt{s.Children}, true
	case *ir.ContextProvider:
		b := [][]ir.Stmt{s.Children}
		return b, b, true
	}
	return nil, nil, false
}

// checkTreelessBody holds a tree-less component to containing no member of any
// family. Placing one is unrestricted (a lifetime bracket belongs in a drawing
// as much as in a layout), and this is what keeps that from being a hole: a
// body that renders a `ui` node has joined that family without saying so, and
// would then be placeable in a canvas.
//
// The mark is what makes a declaration tree-less. A body that named no family
// is not one: inference is reading that same body to give it one, and holding
// it to this rule would refuse every case inference is there to answer.
//
// A boundary and a context override are reached through with the `if` and the
// `for`, which is treeTransparent's list and the same reading
// checkTreeMembership gives them.
func (c *checker) checkTreelessBody(comp *ir.Component, body []ir.Stmt) {
	if comp == nil || !comp.Treeless || comp.AST == nil {
		return
	}
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, st := range stmts {
			if blocks, _, ok := treeTransparent(st); ok {
				for _, b := range blocks {
					walk(b)
				}
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
	walk(body)
}
