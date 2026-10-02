package checker

import (
	"fmt"
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
	// Two grains, two homes. What a *target* can do is read off the two
	// build-tree nodes its pair was selected by; what a *primitive* renders is
	// read off that primitive. A mark written anywhere else resolves and is
	// asked nothing, which is the failure the mark table exists to prevent.
	if g := comp.Gen; g != nil {
		if len(g.Can)+len(g.Cannot)+len(g.Wants) > 0 && !ir.IsBuildTargetTree(ir.TypeFamily(named)) {
			c.error(decl.Pos, "component %s: #[gen.can], #[gen.cannot] and #[gen.wants] say what a target can do, and belong on a build-target node -- one whose return position is build.language or build.platform", comp.Name)
		}
		// A wildcard is a primitive too, and html is why: its widgets are one
		// `element` every tag resolves to rather than a declaration each.
		if g.TargetName != "" && !ir.IsBuildTargetTree(ir.TypeFamily(named)) {
			c.error(decl.Pos, "component %s: #[gen.name] names a build target, and belongs on a build-target node -- one whose return position is build.language or build.platform", comp.Name)
		}
		if len(g.Renders) > 0 && comp.Intrinsic == "" && comp.Wildcard == "" {
			c.error(decl.Pos, "component %s: #[gen.renders] says what a primitive's own nodes support, and belongs on an #[intrinsic] or #[wildcard] declaration", comp.Name)
		}
	}

	// The family of families is the one member of itself: every other family
	// names it in the return position, and it has nothing to name.
	if comp.Builtin == ir.BuiltinTreeFamily {
		if named != nil {
			c.error(decl.Pos, "component %s is the family of families, and a member of itself: it names no family in the return position", comp.Name)
		}
		comp.Tree = comp
		return
	}

	if named == nil {
		if comp.Treeless || c.treeOptional(decl) {
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

	if f := ir.TypeFamily(named); f != nil {
		comp.Tree = f
		if !comp.IsFamily() {
			comp.TreeArgs = familyArgsWritten(f, named.Elems)
		}
		pkg.NoteTreeKind(f)
		if comp.IsFamily() {
			c.checkFamilyDecl(decl, comp)
			return
		}
		// What a family declares a member has to, and the family is
		// registered ahead of it (familiesFirst) but the member's siblings
		// are not all registered yet, so the check waits for the package.
		if len(f.Props) > 0 {
			c.familyMembers = append(c.familyMembers, comp)
		}
		// A build-target node is the target's identity as well as its option
		// schema, and #[gen.name] is the half the world outside SNGL reads.
		if ir.IsBuildTargetTree(f) && (comp.Gen == nil || comp.Gen.TargetName == "") {
			c.error(decl.Pos, "component %s is a build-target node: say what the build calls it with #[gen.name(\"…\")]", comp.Name)
		}
		// A painted shape has nothing to raise an event from. This is drawing's
		// rule rather than one about trees, and it sits here because membership
		// is conferred here -- until a tree can carry rules of its own.
		//
		// A platform primitive is exempt, and in the opposite direction: its
		// event is not the shape raising anything, it is the target calling in
		// with the drawing context, which is how an override says what to
		// paint. Only an #[intrinsic] declaration can be one.
		if ir.IsDrawShapeTree(f) && len(comp.Events) > 0 && comp.Intrinsic == "" {
			c.error(decl.Pos, "component %s: a shape supports no event declarations", comp.Name)
		}
		return
	}
	if named.Kind == ir.TypeTypeParam && slices.ContainsFunc(comp.TypeParams,
		func(tp ir.TypeParam) bool { return tp.Name == named.ParamName }) {
		comp.TreeParam = named.ParamName
		return
	}
	c.error(decl.Pos, "component %s: the return position names the family a component belongs to, and %s is not one",
		comp.Name, named)
}

// checkFamilyDecl holds a family's declaration to the props its members
// share. A value of a family is a record of those props, read off whichever
// member it holds and never bound, so each is a name, a type and perhaps a
// default for the members that omit it: a two-way or const prop, an event, a
// slot would each be read by nobody. A type parameter is the members' to
// bind, each in its return position: `page<T, M>` is a `_page<M>`, and a
// value of the family is read at the type it was bound to. A body is where
// how a family is generated will be written, and nothing reads one yet -- refused rather than dropped, so
// that the first program to write one is not one whose body silently did
// nothing.
func (c *checker) checkFamilyDecl(decl *ast.ComponentDecl, comp *ir.Component) {
	refuse := func(what string) {
		c.error(decl.Pos, "component %s is a family, which declares the props its members share and nothing else: %s", comp.Name, what)
	}
	for _, p := range decl.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			switch {
			case func() bool { _, _, isSlot := ast.SlotType(pd.Type); return isSlot }():
				refuse(pd.Name + " is a slot")
			case pd.Bidirectional:
				refuse(fmt.Sprintf("%q is two-way", pd.Name))
			case pd.Const:
				refuse(fmt.Sprintf("%q is const", pd.Name))
			}
		case ast.EventDecl:
			refuse("@" + pd.Name + " is an event")
		}
	}
	if !comp.Bodyless {
		c.error(decl.Pos, "component %s is a family, and a family has no body yet: remove the block", comp.Name)
	}
}

// checkFamilyMember holds a member to declaring every prop its family
// declares, by name and type. Those props are the whole of what a value of
// the family reads, off whichever member it holds, so a member missing one
// would hand the reader nothing. What the member does with the prop -- a
// default, a two-way binding -- is its own business; a family value reads
// what the prop holds.
//
// A family prop with a default may be omitted, and the member then has it
// with that default and nothing else: a call site cannot set it
// (refuseInheritedProps), since the member never said it takes one.
//
// A member declaring none of them is a group (ir.Component.Group): it renders
// several members rather than being one -- `component extras { nav.page…
// nav.page… }` -- so there is no one value of the family for it to be, and
// what it renders is what a reader reaches.
func (c *checker) checkFamilyMember(comp *ir.Component) {
	f := comp.Tree
	if comp.AST == nil || f == nil || comp.IsFamily() {
		return
	}
	if len(f.Props) > 0 && !slices.ContainsFunc(f.Props, func(fp *ir.Prop) bool {
		return slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == fp.Name })
	}) {
		comp.Group = true
		return
	}
	for _, fp := range f.Props {
		var mp *ir.Prop
		for _, p := range comp.Props {
			if p.Name == fp.Name {
				mp = p
				break
			}
		}
		switch {
		case mp == nil && familyPropDefaulted(f, fp):
			inherited := &ir.Prop{Name: fp.Name, Type: familyPropType(f, comp, fp), Default: fp.Default}
			comp.Props = append(comp.Props, inherited)
			if c.inheritedProps == nil {
				c.inheritedProps = map[*ir.Prop]*ir.Prop{}
			}
			c.inheritedProps[inherited] = fp
		case mp == nil:
			c.error(comp.AST.Pos, "component %s is a member of %s, which declares prop %q: declare %s %s",
				comp.Name, f.Name, fp.Name, fp.Name, fp.Type)
		case mp.Type == nil || fp.Type == nil || !mp.Type.Equal(familyPropType(f, comp, fp)):
			c.error(comp.AST.Pos, "component %s declares prop %q as %s, and its family %s declares it %s",
				comp.Name, fp.Name, mp.Type, familyName(f, comp), familyPropType(f, comp, fp))
		}
	}
}

// familyArgsWritten is the type arguments a return position hands a generic
// family, its defaults filling those it leaves off. Nil for a family that
// takes none.
func familyArgsWritten(f *ir.Component, written []*ir.Type) []*ir.Type {
	if len(f.TypeParams) == 0 {
		return nil
	}
	out := make([]*ir.Type, len(f.TypeParams))
	for i, tp := range f.TypeParams {
		switch {
		case i < len(written):
			out[i] = written[i]
		case tp.Default != nil:
			out[i] = tp.Default
		default:
			out[i] = ir.TypDyn
		}
	}
	return out
}

// familyPropType is the type a member must declare a family prop at: the
// family's, with its type parameters bound as the member's return position
// binds them.
func familyPropType(f, member *ir.Component, fp *ir.Prop) *ir.Type {
	if fp.Type == nil || len(member.TreeArgs) == 0 {
		return fp.Type
	}
	return fp.Type.Substitute(familyBindings(f, member.TreeArgs))
}

// familyBindings binds a generic family's type parameters to args.
func familyBindings(f *ir.Component, args []*ir.Type) map[string]*ir.Type {
	b := map[string]*ir.Type{}
	for i, tp := range f.TypeParams {
		if i < len(args) {
			b[tp.Name] = args[i]
		}
	}
	return b
}

// familyName spells the family a member's return position names, with the
// type arguments it hands it.
func familyName(f, member *ir.Component) string {
	if len(member.TreeArgs) == 0 {
		return f.Name
	}
	return (&ir.Type{Kind: ir.TypeComponent, Decl: f, Elems: member.TreeArgs}).String()
}

// treeOptional reports the declarations an omitted return position is still
// read as "no tree" for, rather than as the missing annotation it now is.
//
// An extension body (`component ui.vbox[platform]`) restates no part of the
// declaration it merges into, so it names no tree either. That is the only
// one: the directives -- `output`, `cache.inputs` -- used to be let off too,
// while the root family lived in `sngl:ui` and `sngl:builtin` could not import
// it, and now name `root` like a window does.
func (c *checker) treeOptional(decl *ast.ComponentDecl) bool {
	return decl.Target != nil || strings.Contains(decl.Name, ".")
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

// refuseInheritedProps reports a call site setting a prop its component took
// from its family's default rather than declaring.
func (c *checker) refuseInheritedProps(pos ast.Pos, comp *ir.Component, bound map[string]bool) {
	if comp == nil || len(c.inheritedProps) == 0 {
		return
	}
	for _, p := range comp.Props {
		if fp, ok := c.inheritedProps[p]; ok && bound[p.Name] {
			c.error(pos, "component %s takes %q from its family %s, which gives it a default and no call site a say: declare %s %s on %s to set it",
				comp.Name, p.Name, comp.Tree.Name, fp.Name, fp.Type, comp.Name)
		}
	}
}

// inheritDefault hands a family prop's checked default to every member that
// omitted the prop, which until now held what pass1 had.
func (c *checker) inheritDefault(fp *ir.Prop) {
	for inherited, from := range c.inheritedProps {
		if from == fp {
			inherited.Default = fp.Default
		}
	}
}

// checkFamilyMembersFrom checks the members this package's registration
// recorded, from mark on. A lib package loads from inside a program's pass1
// and registers its own, so each run drains only what it added.
func (c *checker) checkFamilyMembersFrom(mark int) {
	for _, comp := range c.familyMembers[mark:] {
		c.checkFamilyMember(comp)
	}
	c.familyMembers = c.familyMembers[:mark]
}

// familyPropDefaulted reports whether a family's prop was written with a
// default. A program's default is checked in pass2, after members register,
// so the declaration is what is asked.
func familyPropDefaulted(f *ir.Component, fp *ir.Prop) bool {
	if fp.Default != nil {
		return true
	}
	if f.AST == nil {
		return false
	}
	for _, p := range f.AST.Props.Props {
		if pd, ok := p.(ast.Param); ok && pd.Name == fp.Name {
			return pd.Default != nil
		}
	}
	return false
}
