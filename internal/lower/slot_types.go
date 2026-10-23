package lower

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// slotTypes is what each render slot's synthesized names hold: parent, the
// container its one call site hands it, and elem, the handle of each node its
// body attaches there -- what `__slotN` lists and a placement compares.
type slotTypes struct {
	parent *ir.Type
	elem   *ir.Type
}

// typeSlots records slotTypes for every reactive `if` and `for` in the
// package, read where each is written: the node around it is its parent, and
// the nodes in its body are what it holds. A slot's var is declared on every
// owner that asks for it and its func built by the one that writes it, so the
// answer is the package's, decided before either.
func (st *reactivityState) typeSlots() {
	st.slots = map[string]slotTypes{}
	for _, comp := range st.pkg.Components {
		// Asked only of a component one of whose slots renders into it.
		root := func() *ir.Type { return st.ownerRootType(compOwner{comp}) }
		st.typeSlotsIn(comp.Stmts, root)
		for _, f := range comp.Funcs {
			st.typeSlotsIn(f.Block, root)
		}
	}
	app := func() *ir.Type { return ir.Published().ApplicationType() }
	st.typeSlotsIn(st.pkg.Body, app)
	for _, f := range st.pkg.Funcs {
		st.typeSlotsIn(f.Block, app)
	}
}

// ownerRootType is the type of the container an owner's top-level render
// slots render into: the application for the package body, and for a
// component the box a target makes to hold what its body renders, which is a
// member of the component's family.
func (st *reactivityState) ownerRootType(o reactivityOwner) *ir.Type {
	if c, ok := o.(compOwner); ok {
		return rendersAsType(c.c, nil)
	}
	return ir.Published().ApplicationType()
}

// typeSlotsIn walks stmts as rewriteReactiveStructures does, so the parent
// recorded for a slot is the one its call site is handed.
func (st *reactivityState) typeSlotsIn(stmts []ir.Stmt, parent func() *ir.Type) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			if n.LoweredSlotID != "" {
				st.recordSlotTypes(n.LoweredSlotID, parent(), n.Body, n.Else)
			}
			st.typeSlotsIn(n.Body, parent)
			st.typeSlotsIn(n.Else, parent)
		case *ir.Match:
			for _, arm := range n.Arms {
				st.typeSlotsIn(arm.Body, parent)
			}
		case *ir.For:
			if n.LoweredSlotID != "" {
				st.recordSlotTypes(n.LoweredSlotID, parent(), n.Body, n.Else)
			}
			st.typeSlotsIn(n.Body, parent)
			st.typeSlotsIn(n.Else, parent)
		case *ir.NodeInst:
			st.typeSlotsIn(ir.WidgetChildren(n), func() *ir.Type { return handleType(n) })
			for _, h := range n.Handlers {
				if h.Func != nil {
					st.typeSlotsIn(h.Func.Block, parent)
				}
			}
		case *ir.SlotInst:
			st.typeSlotsIn(n.Children, parent)
		case *ir.ErrorBoundary:
			st.typeSlotsIn(n.Children, parent)
		}
	}
}

func (st *reactivityState) recordSlotTypes(slotID string, parent *ir.Type, body, els []ir.Stmt) {
	if _, ok := st.slots[slotID]; ok {
		return
	}
	var elem *ir.Type
	hosted := hostedBy(parent)
	st.eachAttached(body, hosted, func(t *ir.Type) { elem = unifyHandles(elem, t) })
	st.eachAttached(els, hosted, func(t *ir.Type) { elem = unifyHandles(elem, t) })
	st.slots[slotID] = slotTypes{parent: parent, elem: elem}
}

// eachAttached calls f with the type of each node a slot body attaches to its
// parent, as renderSlotBody emits them: the body's own nodes, through the
// `if`s, `for`s and boundaries around them.
func (st *reactivityState) eachAttached(stmts []ir.Stmt, hosted *ir.Component, f func(*ir.Type)) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if st.declState().instanceRecords && isInstanceNode(n) {
				f(rendersAsType(n.Component, hosted))
			} else {
				f(handleType(n))
			}
		case *ir.If, *ir.For, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				st.eachAttached(*b, hosted, f)
			}
		case *ir.ErrorBoundary:
			st.eachAttached(n.Children, hosted, f)
		}
	}
}

// unifyHandles is the type both a and b are: the one node type where they
// name one, and otherwise the family both are members of.
func unifyHandles(a, b *ir.Type) *ir.Type {
	if a == nil {
		return b
	}
	if a.Decl == b.Decl {
		return a
	}
	fa, fb := handleFamily(a), handleFamily(b)
	if fa == nil || fa != fb {
		panic(fmt.Sprintf("lower: a render slot attaches %s and %s, which share no family", a, b))
	}
	return &ir.Type{Kind: ir.TypeComponent, Decl: fa}
}

// handleFamily is the family a handle's node is a member of, or the family a
// family value already names.
func handleFamily(t *ir.Type) *ir.Component {
	c, ok := t.Decl.(*ir.Component)
	if !ok || c == nil {
		return nil
	}
	if c.IsFamily() {
		return c
	}
	return c.Family
}

// slotElemType is what slotID's list holds.
func (st *reactivityState) slotElemType(slotID string) *ir.Type {
	t := st.slots[slotID].elem
	if t == nil {
		panic(fmt.Sprintf("lower: render slot %s attaches no node", slotID))
	}
	return t
}

// slotParentType is the container slotID's call site hands it.
func (st *reactivityState) slotParentType(slotID string) *ir.Type {
	t, ok := st.slots[slotID]
	if !ok {
		panic(fmt.Sprintf("lower: render slot %s was never typed", slotID))
	}
	return t.parent
}
