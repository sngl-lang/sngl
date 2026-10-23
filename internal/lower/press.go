package lower

import (
	"strings"

	"duckfam.us/sngl/ir"
)

// NamePresses answers a test's trigger of an event through callsite's `#id`
// where body, the component's body being spliced in its place, emits the event
// from a node one loop renders, handing on the loop's index (ir.PressedNode):
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
// body the target does not override, and passInlinePure. The node is named
// at once and the triggers rewritten by the returned commit, which a caller
// runs only once the splice is kept: the optimizer may still give one up. A
// trigger a splice before this one rewrote names the node too, so a second
// component's strip under the same id is named as the first was.
func NamePresses(pkg *ir.Package, body []ir.Stmt, callsite *ir.NodeInst) (commit func()) {
	commit = func() {}
	if pkg == nil || callsite == nil || callsite.ID == "" {
		return commit
	}
	waiting, pressed := triggersThrough(pkg, callsite)
	events := map[string]bool{}
	for _, c := range waiting {
		events[c.Event] = true
	}
	for e := range pressed {
		events[e] = true
	}
	fires := map[string]string{}
	for event := range events {
		n, _, press := ir.PressedNode(body, event)
		if n == nil {
			continue
		}
		id := pressID(callsite.ID, event)
		if n.ID != id {
			n.ID = id
			n.Handle = &ir.Var{Name: id, Type: n.Component.SymType(), IsConst: true, NodeHandle: true}
		}
		fires[event] = press
	}
	return func() {
		for _, c := range waiting {
			press, ok := fires[c.Event]
			if !ok {
				continue
			}
			c.Receiver = renamedRef(c.Receiver, pressID(callsite.ID, c.Event), c.Args[0].Value)
			c.Event, c.Args = press, nil
		}
	}
}

// pressID is the `#id` the node a trigger of event through id presses is
// given.
func pressID(id, event string) string { return id + "__" + event }

// triggersThrough is each trigger in pkg's tests of an event with one argument
// through the `#id` callsite wrote, reached from the instance a test holds or
// through instances it renders (`c.inner.strip`), and each event a trigger
// through it was already turned into a press of.
func triggersThrough(pkg *ir.Package, callsite *ir.NodeInst) (waiting []*ir.Call, pressed map[string]bool) {
	pressed = map[string]bool{}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			c, ok := n.(*ir.Call)
			if !ok || c.Event == "" {
				return nil
			}
			sel := triggerSelect(c.Receiver)
			if sel == nil {
				return nil
			}
			if len(c.Args) == 1 && sel.Field == callsite.ID && sameComponent(sel.Type, callsite.Component) {
				waiting = append(waiting, c)
			} else if event, ok := strings.CutPrefix(sel.Field, callsite.ID+"__"); ok && len(c.Args) == 0 {
				pressed[event] = true
			}
			return nil
		})
	}
	return waiting, pressed
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

// renamedRef is the ref r, a trigger's receiver, naming the copy at idx of the
// node id one loop renders inside r's node: the indexes into the loops around
// r's node kept, a narrowing after a null test dropped.
func renamedRef(r ir.Expr, id string, idx ir.Expr) ir.Expr {
	inner := renamedSelect(r, id)
	t := inner.ExprType()
	if t != nil && t.Kind == ir.TypeList && len(t.Elems) > 0 {
		t = t.Elems[0]
	}
	return &ir.Index{Operand: inner, Idx: idx, Type: t}
}

func renamedSelect(r ir.Expr, id string) ir.Expr {
	switch x := r.(type) {
	case *ir.Conversion:
		return renamedSelect(x.Operand, id)
	case *ir.Index:
		return &ir.Index{Operand: renamedSelect(x.Operand, id), Idx: x.Idx, Type: x.Type}
	case *ir.Select:
		sel := *x
		sel.Field = id
		sel.Ref = composeRef(x.Ref, ir.RefCount([]byte{ir.RefList, '1'}))
		return &sel
	}
	return r
}
