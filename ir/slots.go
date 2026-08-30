package ir

import "fmt"

// SlotSplicer projects what a call site supplied into the insertion points a
// component's body declares. The optimizer and the inliner both do this, over
// IR they clone and bind differently -- one keys a parameter by pointer, the
// other by name -- so the two operations they differ in are supplied and the
// walk itself is written once. The two copies had already drifted -- one
// recursed into a ContextProvider's children and the other treated it as a
// leaf -- which is the divergence this shape exists to prevent, whichever of
// the two a given program reaches first.
type SlotSplicer struct {
	// Clone copies a body before it is spliced. Two insertions of one slot
	// would otherwise alias the same IR nodes.
	Clone func([]Stmt) []Stmt
	// Bind substitutes a scoped slot's arguments for the parameters the
	// populator declared, over a body Clone has already copied.
	Bind func(body []Stmt, sc *SlotContent, si *SlotInst) []Stmt
}

// Substitute replaces every SlotInst in stmts with what callsite supplied.
func (sp SlotSplicer) Substitute(stmts []Stmt, callsite *NodeInst) []Stmt {
	out := make([]Stmt, 0, len(stmts))
	for _, s := range stmts {
		if si, isSlot := s.(*SlotInst); isSlot {
			out = append(out, sp.body(si, callsite)...)
			continue
		}
		switch n := s.(type) {
		case *If:
			n.Body = sp.Substitute(n.Body, callsite)
			n.Else = sp.Substitute(n.Else, callsite)
		case *For:
			n.Body = sp.Substitute(n.Body, callsite)
			n.Else = sp.Substitute(n.Else, callsite)
		case *NodeInst:
			n.Children = sp.Substitute(n.Children, callsite)
		case *ErrorBoundary:
			n.Children = sp.Substitute(n.Children, callsite)
		case *ContextProvider:
			n.Children = sp.Substitute(n.Children, callsite)
		case *Window:
			n.Body = sp.Substitute(n.Body, callsite)
		case *Assign, *LocalVar, *Return, *CallStmt, *Emit, *Toggle, *CanvasRedrawStmt:
			// Leaf stmts -- no nested SlotInsts.
		default:
			panic(fmt.Sprintf("SlotSplicer.Substitute: unhandled %T", n))
		}
		out = append(out, s)
	}
	return out
}

// body is what one insertion point renders: the content the call site supplied
// for it, or the insertion's own block as the fallback when it supplied none.
//
// A scoped slot's arguments are bound here rather than at the call site,
// because the names they bind to are the populator's and the values are the
// insertion's -- the two only meet once the body is being spliced into place.
func (sp SlotSplicer) body(si *SlotInst, callsite *NodeInst) []Stmt {
	sc := callsite.Slots[si.Name]
	if sc == nil {
		// The default slot's content arrives as ordinary children rather than
		// through the map.
		if si.Name == DefaultSlot && len(callsite.Children) > 0 {
			return sp.Clone(callsite.Children)
		}
		return sp.Clone(si.Children)
	}
	return sp.Bind(sp.Clone(sc.Body), sc, si)
}
