package checker

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// bindFamily reads a component's return position as the tree it is a member
// of, which is the whole of what that position says, and returns what the
// position named. Naming one of the component's own type parameters makes it
// a wrapper whose family is whatever it was handed; naming something that is
// not a tree is an error rather than a children contract -- a slot is what
// declares those.
//
// Bound as the component registers, because a family's members register
// after it (familiesFirst) and are measured against it. Nothing here reads a
// mark but the family of families' kind, which registration reads off the
// source (writtenBuiltinKind); what the marks say about the tree is
// finishTreeMarks', once they have run.
func (c *checker) bindFamily(decl *ast.ComponentDecl, comp *ir.Component, pkg *ir.Package) *ir.Type {
	if decl == nil || comp == nil {
		return nil
	}
	named := comp.ChildrenType
	comp.ChildrenType = nil

	// The family of families is the one member of itself: every other family
	// names it in the return position, and it has nothing to name.
	if comp.Builtin == ir.BuiltinFamilyOfFamilies {
		comp.Family = comp
		return named
	}
	if named == nil {
		return nil
	}
	// A component naming itself is data: a record of its props, its own
	// family and the only member of it.
	if named.Kind == ir.TypeComponent && named.Decl == comp {
		comp.Family, comp.Data = comp, true
		return named
	}
	if f := ir.TypeFamily(named); f != nil {
		if f.Data && comp.Bodyless {
			c.error(decl.Pos, "component %s names %s as its family, which is data: only %s itself is a record, and a component composing them has a body", comp.Name, f.Name, f.Name)
			return named
		}
		comp.Family = f
		if !comp.IsFamily() {
			comp.FamilyTypeArgs = familyArgsWritten(f, named.Elems)
		}
		pkg.NoteFamilyKind(f)
		if comp.IsFamily() {
			c.checkFamilyDecl(decl, comp)
			return named
		}
		// What a family declares a member has to, and the family is
		// registered ahead of it (familiesFirst) but the member's siblings
		// are not all registered yet, so the check waits for the package.
		if len(f.Props) > 0 {
			c.familyMembers = append(c.familyMembers, comp)
		}
		return named
	}
	if named.Kind == ir.TypeTypeParam && slices.ContainsFunc(comp.TypeParams,
		func(tp ir.TypeParam) bool { return tp.Name == named.ParamName }) {
		comp.FamilyParam = named
		return named
	}
	c.error(decl.Pos, "component %s: the return position names the family a component belongs to, and %s is not one",
		comp.Name, named)
	return named
}

// finishFamilyMarks holds what a component's marks say about its tree to the
// return position bindTree read, once the marks have run. named is what that
// position named, nil for nothing.
//
// Naming nothing hands the question to inferComponentTrees, which reads the
// answer off the body once every body has been read. Belonging to no family is
// a claim the mark makes, and silence is no longer how it is spelled.
func (c *checker) finishFamilyMarks(decl *ast.ComponentDecl, comp *ir.Component, named *ir.Type) {
	if decl == nil || comp == nil {
		return
	}
	// Where a sngl:x/gen mark may be written. Two grains, two homes. What a
	// *target* can do is read off the two build-tree nodes its pair was
	// selected by; what a *primitive* renders is read off that primitive. A
	// mark written anywhere else resolves and is asked nothing, which is the
	// failure the mark table exists to prevent.
	if g := comp.Gen; g != nil {
		if len(g.Can)+len(g.Cannot)+len(g.Wants) > 0 && !ir.IsBuildTargetFamily(ir.TypeFamily(named)) {
			c.error(decl.Pos, "component %s: #[gen.can], #[gen.cannot] and #[gen.wants] say what a target can do, and belong on a build-target node -- one whose return position is build.language or build.platform", comp.Name)
		}
		// A wildcard is a primitive too, and html is why: its widgets are one
		// `element` every tag resolves to rather than a declaration each.
		if g.TargetName != "" && !ir.IsBuildTargetFamily(ir.TypeFamily(named)) {
			c.error(decl.Pos, "component %s: #[gen.name] names a build target, and belongs on a build-target node -- one whose return position is build.language or build.platform", comp.Name)
		}
		if g.Native != "" && !ir.IsBuildTargetFamily(ir.TypeFamily(named)) {
			c.error(decl.Pos, "component %s: #[gen.native] names the Go generator of a build target, and belongs on a build-target node -- one whose return position is build.language or build.platform", comp.Name)
		}
		if len(g.Renders) > 0 && !comp.Primitive() {
			c.error(decl.Pos, "component %s: #[gen.renders] says what a primitive's own nodes support, and belongs on an #[intrinsic], #[wildcard] or native declaration", comp.Name)
		}
	}

	if comp.Builtin == ir.BuiltinFamilyOfFamilies {
		if named != nil {
			c.error(decl.Pos, "component %s is the family of families, and a member of itself: it names no family in the return position", comp.Name)
		}
		return
	}

	if named == nil {
		if c.familyOptional(decl) {
			return
		}
		if c.inLibSource() {
			// A library's declarations are a published contract, which a body
			// should not be quietly restating -- and a bodyless one, the common
			// case in lib/, has nothing to read an answer off. Every tier says
			// it.
			c.error(decl.Pos, "component %s: name the family it belongs to in the return position, or return a type parameter, `<F>(…) F`, to take the family it is placed in",
				comp.Name)
			return
		}
		c.inferFamilies = append(c.inferFamilies, comp)
		return
	}
	f := ir.TypeFamily(named)
	if f == nil || comp.IsFamily() {
		return
	}
	// A build-target node is the target's identity as well as its option
	// schema, and #[gen.name] is the half the world outside SNGL reads.
	if ir.IsBuildTargetFamily(f) && (comp.Gen == nil || comp.Gen.TargetName == "") {
		c.error(decl.Pos, "component %s is a build-target node: say what the build calls it with #[gen.name(\"…\")]", comp.Name)
	}
	// A family that says its members raise nothing (#[tree.eventless]):
	// a handler written for one would never run. A platform primitive is
	// exempt, its event being the target calling in.
	if f.Eventless && len(comp.Events) > 0 && !comp.Primitive() {
		c.error(decl.Pos, "component %s: a member of %s raises no events, so it declares none", comp.Name, f.Name)
	}
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
// A member declaring none of them is no value of the family
// (ir.IsFamilyValue): it composes members rather than being one --
// `component extras { nav.page… nav.page… }` -- and what it renders is what a
// reader reaches, once it is inlined where the family's members are
// collected.
func (c *checker) checkFamilyMember(comp *ir.Component) {
	f := comp.Family
	// A data family's props are its record's fields, and what composes its
	// records is no value of it, whichever of those props it also declares.
	if comp.AST == nil || f == nil || comp.IsFamily() || f.Data {
		return
	}
	if len(f.Props) > 0 && !slices.ContainsFunc(f.Props, func(fp *ir.Prop) bool {
		return slices.ContainsFunc(comp.Props, func(p *ir.Prop) bool { return p.Name == fp.Name })
	}) {
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
			t := c.tables()
			if t.inheritedProps == nil {
				t.inheritedProps = map[*ir.Prop]*ir.Prop{}
			}
			t.inheritedProps[inherited] = fp
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
//
// A member whose family was inferred from its body wrote no return position,
// so the family's defaults bind it, as a written `_page` with no arguments
// would.
func familyPropType(f, member *ir.Component, fp *ir.Prop) *ir.Type {
	if fp.Type == nil || len(f.TypeParams) == 0 {
		return fp.Type
	}
	args := member.FamilyTypeArgs
	if len(args) == 0 {
		args = familyArgsWritten(f, nil)
	}
	return fp.Type.Substitute(familyBindings(f, args))
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
	if len(member.FamilyTypeArgs) == 0 {
		return f.Name
	}
	return (&ir.Type{Kind: ir.TypeComponent, Decl: f, Elems: member.FamilyTypeArgs}).String()
}

// familyOptional reports the declarations an omitted return position is still
// read as "no tree" for, rather than as the missing annotation it now is.
//
// An extension body (`component ui.vbox[platform]`) restates no part of the
// declaration it merges into, so it names no tree either. That is the only
// one: the directives -- `output`, `cache.inputs` -- used to be let off too,
// while the root family lived in `sngl:ui` and `sngl:builtin` could not import
// it, and now name `root` like a window does.
func (c *checker) familyOptional(decl *ast.ComponentDecl) bool {
	return decl.Target != nil || strings.Contains(decl.Name, ".")
}

// familyTransparent reports the blocks a statement carries the tree question
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
func familyTransparent(st ir.Stmt) (all, binds [][]ir.Stmt, ok bool) {
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

// checkFamilyParamBody holds a tree-less component to containing no member of any
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
func (c *checker) checkFamilyParamBody(comp *ir.Component, body []ir.Stmt) {
	if comp == nil || comp.FamilyParam == nil || comp.AST == nil {
		return
	}
	const says = "takes the family it is placed in"
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, st := range stmts {
			if blocks, _, ok := familyTransparent(st); ok {
				for _, b := range blocks {
					walk(b)
				}
				continue
			}
			// A slot insertion puts what the slot takes where it stands, so a
			// slot of widgets inserted here renders widgets as surely as a
			// node would. Passed over, a tree-less wrapper carried them into
			// any tree it was placed in.
			if si, ok := st.(*ir.SlotInst); ok {
				if got := c.insertedFamily(comp, si); got != nil && !si.Crosses {
					at := comp.AST.Pos
					if si.AST != nil {
						at = si.AST.Pos
					}
					c.error(at, "component %s %s, so it may not contain the %s slot %s", comp.Name, says, got.Name, si.Name)
				}
				continue
			}
			ni, ok := st.(*ir.NodeInst)
			if !ok || ni.Crosses || ni.Component == nil || ni.Component.Family == nil {
				continue
			}
			at := comp.AST.Pos
			if sp := stmtPos(ni.AST); sp != nil {
				at = *sp
			}
			c.error(at, "component %s %s, so it may not contain the %s component %s",
				comp.Name, says, ni.Component.Family.Name, ni.Component.Name)
		}
	}
	walk(body)
}

// refuseInheritedProps reports a call site setting a prop its component took
// from its family's default rather than declaring.
func (c *checker) refuseInheritedProps(pos ast.Pos, comp *ir.Component, bound map[string]bool) {
	if comp == nil {
		return
	}
	for _, p := range comp.Props {
		if fp := c.inheritedFrom(p); fp != nil && bound[p.Name] {
			c.error(pos, "component %s takes %q from its family %s, which gives it a default and no call site a say: declare %s %s on %s to set it",
				comp.Name, p.Name, comp.Family.Name, fp.Name, fp.Type, comp.Name)
		}
	}
}

// inheritDefault hands a family prop's checked default to every member that
// omitted the prop, which until now held what pass1 had.
func (c *checker) inheritDefault(fp *ir.Prop) {
	for inherited, from := range c.tables().inheritedProps {
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

// unbounded is renderedRange's "no most": a loop renders any number.
const unbounded = -1

// renderedRange is how many nodes a block renders, at least lo and at most hi
// (unbounded for no bound), which is what a slot's count is measured against.
// The statements written are not the count: a `for` is one statement and any
// number of nodes, an `if` with no `else` none or one, and a `var` none.
//
// owner is the component the block was written in, whose slots an insertion
// in it names.
func (c *checker) renderedRange(owner *ir.Component, stmts []ir.Stmt) (lo, hi int) {
	for _, st := range stmts {
		l, h := c.stmtRange(owner, st)
		lo += l
		if hi != unbounded {
			if h == unbounded {
				hi = unbounded
			} else {
				hi += h
			}
		}
	}
	return lo, hi
}

func (c *checker) stmtRange(owner *ir.Component, st ir.Stmt) (lo, hi int) {
	switch s := st.(type) {
	case *ir.NodeInst:
		return 1, 1
	case *ir.If:
		return c.eitherRange(owner, s.Body, s.Else)
	case *ir.ErrorBoundary:
		return c.eitherRange(owner, s.Children, s.Failed)
	case *ir.ContextProvider:
		return c.renderedRange(owner, s.Children)
	case *ir.For:
		bl, bh := c.renderedRange(owner, s.Body)
		el, eh := c.renderedRange(owner, s.Else)
		if bh == 0 {
			return 0, eh
		}
		// An empty iterable renders the else; any other renders the body at
		// least once.
		return min(bl, el), unbounded
	case *ir.SlotInst:
		decl := c.entrySpec[s]
		if decl == nil {
			decl = s.Entry
		}
		if decl == nil {
			decl = ownerSlot(owner, s.Name)
		}
		if decl == nil {
			return 0, unbounded
		}
		switch decl.Card {
		case ir.SlotOne:
			return 1, 1
		case ir.SlotOptional:
			_, fh := c.renderedRange(owner, s.Children)
			if fh == unbounded {
				return 0, unbounded
			}
			return 0, max(1, fh)
		}
		return 0, unbounded
	}
	return 0, 0
}

// eitherRange is the range of a statement that renders one of two blocks.
func (c *checker) eitherRange(owner *ir.Component, a, b []ir.Stmt) (lo, hi int) {
	al, ah := c.renderedRange(owner, a)
	bl, bh := c.renderedRange(owner, b)
	if ah == unbounded || bh == unbounded {
		return min(al, bl), unbounded
	}
	return min(al, bl), max(ah, bh)
}

// fitsCard reports whether a block rendering lo to hi nodes fits a slot's
// count: exactly one, or none or one. Any number fits any.
func fitsCard(card ir.SlotCard, lo, hi int) bool {
	switch card {
	case ir.SlotOne:
		return lo == 1 && hi == 1
	case ir.SlotOptional:
		return hi != unbounded && hi <= 1
	}
	return true
}
