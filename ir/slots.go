package ir

import (
	"fmt"
	"slices"
)

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
		if si, isSlot := s.(*SlotInst); isSlot && si.Entry == nil {
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
			for _, name := range SlotNames(n.Slots) {
				n.Slots[name].Body = sp.Substitute(n.Slots[name].Body, callsite)
			}
		case *ErrorBoundary:
			n.Children = sp.Substitute(n.Children, callsite)
			n.Failed = sp.Substitute(n.Failed, callsite)
		case *ContextProvider:
			n.Children = sp.Substitute(n.Children, callsite)
		case *Assign, *LocalVar, *Return, *CallStmt, *Emit, *Toggle, *CanvasRedrawStmt,
			*Break, *Continue:
			// Leaf stmts -- no nested SlotInsts.
		case *SlotInst:
			// A population's entry, which entries substitutes when the
			// population is spliced for the insertion that supplied it.
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
	bound := sp.Bind(sp.Clone(stmts), sc, si)
	if si.Decl == nil || len(si.Decl.Slots) == 0 {
		return bound
	}
	return sp.entries(bound, si, callsite)
}

// entries replaces, in a population spliced for ins, every insertion of one of
// ins's component entries with what ins populated it with. That content was
// written beside ins, in the callee's body, so the callee's own slots in it
// still answer to callsite.
func (sp SlotSplicer) entries(stmts []Stmt, ins *SlotInst, callsite *NodeInst) []Stmt {
	out := make([]Stmt, 0, len(stmts))
	for _, s := range stmts {
		if x, ok := s.(*SlotInst); ok && x.Entry != nil && slices.Contains(ins.Decl.Slots, x.Entry) {
			out = append(out, sp.entry(x, ins, callsite)...)
			continue
		}
		switch n := s.(type) {
		case *If:
			n.Body = sp.entries(n.Body, ins, callsite)
			n.Else = sp.entries(n.Else, ins, callsite)
		case *For:
			n.Body = sp.entries(n.Body, ins, callsite)
			n.Else = sp.entries(n.Else, ins, callsite)
		case *NodeInst:
			n.Children = sp.entries(n.Children, ins, callsite)
			for _, name := range SlotNames(n.Slots) {
				n.Slots[name].Body = sp.entries(n.Slots[name].Body, ins, callsite)
			}
		case *SlotInst:
			n.Children = sp.entries(n.Children, ins, callsite)
			for _, name := range SlotNames(n.Slots) {
				n.Slots[name].Body = sp.entries(n.Slots[name].Body, ins, callsite)
			}
		case *ErrorBoundary:
			n.Children = sp.entries(n.Children, ins, callsite)
			n.Failed = sp.entries(n.Failed, ins, callsite)
		case *ContextProvider:
			n.Children = sp.entries(n.Children, ins, callsite)
		}
		out = append(out, s)
	}
	return out
}

func (sp SlotSplicer) entry(x, ins *SlotInst, callsite *NodeInst) []Stmt {
	content := ins.Slots[x.Entry.Name]
	if content == nil {
		return sp.entries(x.Children, ins, callsite)
	}
	body := sp.Bind(sp.Clone(content.Body), content, x)
	return sp.Substitute(body, callsite)
}

// Supplied is what a call site hands one slot of its component: a population
// by name (Content non-nil) or the bare children for the rest slot.
type Supplied struct {
	Decl    *SlotDecl
	Content *SlotContent
	Body    []Stmt
}

// SuppliedContent is everything n hands its component, in the order the
// component declares its slots. A node whose component declares none has only
// its children, under no declaration.
func SuppliedContent(n *NodeInst) []Supplied {
	if n.Component == nil || len(n.Component.Slots) == 0 {
		if len(n.Children) == 0 {
			return nil
		}
		return []Supplied{{Body: n.Children}}
	}
	var out []Supplied
	for _, s := range n.Component.Slots {
		switch sc := n.Slots[s.Name]; {
		case sc != nil:
			out = append(out, Supplied{Decl: s, Content: sc, Body: sc.Body})
		case s.Rest && len(n.Children) > 0:
			out = append(out, Supplied{Decl: s, Body: n.Children})
		}
	}
	return out
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
