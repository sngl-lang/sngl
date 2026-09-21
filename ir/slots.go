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
			n.Failed = sp.Substitute(n.Failed, callsite)
		case *ContextProvider:
			n.Children = sp.Substitute(n.Children, callsite)
		case *Assign, *LocalVar, *Return, *CallStmt, *Emit, *Toggle, *CanvasRedrawStmt,
			*Break, *Continue:
			// Leaf stmts -- no nested SlotInsts.
		default:
			panic(fmt.Sprintf("SlotSplicer.Substitute: unhandled %T", n))
		}
		out = append(out, s)
	}
	return out
}

// body is what one insertion point renders, cloned and bound for splicing.
func (sp SlotSplicer) body(si *SlotInst, callsite *NodeInst) []Stmt {
	stmts, sc, _ := SlotBody(si, callsite)
	if sc == nil {
		return sp.Clone(stmts)
	}
	return sp.Bind(sp.Clone(stmts), sc, si)
}

// SlotBody reports what one insertion point renders: the content the call site
// supplied for it, or the insertion's own block as the fallback when it
// supplied none.
//
// supplied is what tells a renderer whose scope to evaluate the body in.
// Content written at the call site reads the caller's scope; an insertion's own
// fallback reads the component's. A splicer flattens both into one scope and so
// does not need the distinction, but an interpreter evaluating in place does --
// which is why the decision lives here and the two share it rather than each
// deciding for itself. The two copies of the walk above had already drifted
// once; this is the same hazard one level down.
//
// sc is non-nil only for content that arrived through the named-slot map, and
// carries the parameter names a scoped slot binds its arguments to. Children
// written bare have no binding site to have written names at, so they see no
// parameters at all -- a caller that wants them writes the population. A nil
// callsite is a component instantiated with nothing supplied at all.
func SlotBody(si *SlotInst, callsite *NodeInst) (body []Stmt, sc *SlotContent, supplied bool) {
	if callsite == nil {
		return si.Children, nil, false
	}
	if c := callsite.Slots[si.Name]; c != nil {
		return c.Body, c, true
	}
	// The rest slot's content arrives as ordinary children rather than through
	// the map.
	if si.Rest && len(callsite.Children) > 0 {
		return callsite.Children, nil, true
	}
	return si.Children, nil, false
}
