package lower

import (
	"duckfam.us/sngl/ir"
)

// NamePresses answers a test's trigger of an event through callsite's `#id`
// where body, the component's body being spliced in its place, emits the event
// from a node a loop renders, handing on the loop's index (ir.EmittedIndex):
// ui.tabs' strip, whose tab at k emits `change(k)` when clicked. The id lands
// on the body's first node, which reports none of it, so the trigger is a
// press: the node is given an `#id` of its own, `<id>__<event>`, and each such
// trigger in pkg's tests becomes a press of its copy at the index the trigger
// handed -- `c.strip.change(1)` a click of the tab at 1, which writes
// `selected` and emits the change, as a user's click does, through the
// invoker every target has for one copy of a node loops render.
//
// Both inliners call it before the emits are substituted, which is where the
// index an emit hands on is still in the body: the optimizer, which splices a
// body the target does not override, and passInlinePure.
func NamePresses(pkg *ir.Package, body []ir.Stmt, callsite *ir.NodeInst) {
	if pkg == nil || callsite == nil || callsite.ID == "" {
		return
	}
	for _, c := range triggersThrough(pkg, callsite) {
		if reportsAtRoot(body, c.Event) {
			continue
		}
		n, fires := pressedNode(body, c.Event, nil)
		if n == nil {
			continue
		}
		id := callsite.ID + "__" + c.Event
		if n.ID != id {
			n.ID = id
			n.Handle = &ir.Var{Name: id, Type: n.Component.SymType(), IsConst: true, NodeHandle: true}
		}
		c.Receiver = &ir.Index{Operand: renamedRef(c.Receiver, id), Idx: c.Args[0].Value}
		c.Event, c.Args = fires, nil
	}
}

// triggersThrough is each trigger in pkg's tests of an event with one
// argument through the `#id` callsite wrote, reached from the instance a test
// holds or through instances it renders (`c.inner.strip`).
func triggersThrough(pkg *ir.Package, callsite *ir.NodeInst) []*ir.Call {
	var out []*ir.Call
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			c, ok := n.(*ir.Call)
			if !ok || c.Event == "" || len(c.Args) != 1 {
				return nil
			}
			if sel := triggerSelect(c.Receiver); sel != nil && sel.Field == callsite.ID && sameComponent(sel.Type, callsite.Component) {
				out = append(out, c)
			}
			return nil
		})
	}
	return out
}

// triggerSelect is the select naming the node a trigger's receiver reads, under
// the indexes into the loops around it and a narrowing after a null test.
func triggerSelect(r ir.Expr) *ir.Select {
	for {
		switch x := r.(type) {
		case *ir.Conversion:
			r = x.Operand
		case *ir.Index:
			r = x.Operand
		case *ir.Select:
			return x
		default:
			return nil
		}
	}
}

// sameComponent reports whether t, a node ref's type under whatever lists and
// options its count wraps it in, is an instance of comp: of the declaration,
// or of the copy a target's override was composed into.
func sameComponent(t *ir.Type, comp *ir.Component) bool {
	for t != nil && (t.Kind == ir.TypeList || t.Kind == ir.TypeOption) && len(t.Elems) > 0 {
		t = t.Elems[0]
	}
	if t == nil || comp == nil {
		return false
	}
	c, ok := t.Decl.(*ir.Component)
	return ok && c.Name == comp.Name && c.PkgPath() == comp.PkgPath()
}

// reportsAtRoot reports whether the node a call site's `#id` lands on, the
// body's first, handles event itself: a select's control, which a trigger
// reaches directly.
func reportsAtRoot(body []ir.Stmt, event string) bool {
	for _, s := range body {
		if n, ok := s.(*ir.NodeInst); ok {
			for _, h := range n.Handlers {
				if h.Name == event {
					return true
				}
			}
			return false
		}
	}
	return false
}

// pressedNode is the first node of stmts, inside the loops around it, whose
// handler or callback prop emits event with the index of the innermost loop's
// copy, and what pressing it fires: the handler's event, or a click for a
// callback, which is what calls one.
func pressedNode(stmts []ir.Stmt, event string, loop *ir.For) (*ir.NodeInst, string) {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.For:
			if n, fires := pressedNode(x.Body, event, x); n != nil {
				return n, fires
			}
		case *ir.If:
			if n, fires := pressedNode(x.Body, event, loop); n != nil {
				return n, fires
			}
			if n, fires := pressedNode(x.Else, event, loop); n != nil {
				return n, fires
			}
		case *ir.NodeInst:
			if loop != nil && loop.KeySym != nil && x.Component != nil {
				for _, h := range x.Handlers {
					if h.Func != nil && ir.EmittedIndex(h.Func.Block, event) == loop.KeySym {
						return x, h.Name
					}
				}
				for _, p := range x.Props {
					if l, ok := p.Value.(*ir.Lambda); ok && l.Func != nil && ir.EmittedIndex(l.Func.Block, event) == loop.KeySym {
						return x, "click"
					}
				}
			}
			if n, fires := pressedNode(x.Children, event, loop); n != nil {
				return n, fires
			}
		}
	}
	return nil, ""
}

// renamedRef is the ref r, a trigger's receiver, naming the node id inside one
// more loop: the indexes into the loops around r's node kept, a narrowing
// after a null test dropped.
func renamedRef(r ir.Expr, id string) ir.Expr {
	switch x := r.(type) {
	case *ir.Conversion:
		return renamedRef(x.Operand, id)
	case *ir.Index:
		return &ir.Index{Operand: renamedRef(x.Operand, id), Idx: x.Idx}
	case *ir.Select:
		sel := *x
		sel.Field = id
		sel.Ref = composeRef(x.Ref, ir.RefCount([]byte{ir.RefList, '1'}))
		return &sel
	}
	return r
}
