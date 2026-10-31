package lower

import (
	"slices"
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
	windowPresses(pkg, body, callsite)
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

// windowPresses answers a test's trigger of a window's event that takes
// nothing -- `c.main.back()` -- where body draws what fires it as a node of its
// own, outside any loop: the way back fyne and gtk4 show while the window can
// go back. The node is named `<id>__<event>` and the trigger becomes a press
// of it, so the window answers as a user's press does: with nothing while it
// draws no way back.
func windowPresses(pkg *ir.Package, body []ir.Stmt, callsite *ir.NodeInst) {
	if callsite.Component == nil || callsite.Component.Family == nil || !ir.IsRootFamily(callsite.Component.Family) {
		return
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			c, ok := n.(*ir.Call)
			if !ok || c.Event == "" || len(c.Args) != 0 {
				return nil
			}
			sel := triggerSelect(c.Receiver)
			if sel == nil || sel.Field != callsite.ID || !sameComponent(sel.Type, callsite.Component) {
				return nil
			}
			if ir.ReportsAtRoot(body, c.Event) {
				return nil
			}
			node := emittingNode(body, c.Event)
			if node == nil {
				return nil
			}
			id := pressID(callsite.ID, c.Event)
			if node.ID != id {
				node.ID = id
				node.Handle = &ir.Var{Name: id, Type: node.Component.SymType(), IsConst: true, NodeHandle: true}
			}
			r := *sel
			r.Field, r.Type = id, node.Component.SymType()
			// The event stays the window's: a target names the press of a
			// handler that re-raises one for the event it raises
			// (codegen.TriggerEventName).
			c.Receiver = &r
			return nil
		})
	}
}

// emittingNode is the first node of body outside any loop whose handler
// emits event with nothing.
func emittingNode(body []ir.Stmt, event string) *ir.NodeInst {
	var node *ir.NodeInst
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if node != nil {
				return
			}
			if _, loop := s.(*ir.For); loop {
				continue
			}
			if x, ok := s.(*ir.NodeInst); ok && x.Component != nil {
				for _, h := range x.Handlers {
					if h.Func != nil && emitsBare(h.Func.Block, event) {
						node = x
						return
					}
				}
			}
			for _, b := range ir.ViewBlocks(s) {
				walk(*b)
			}
		}
	}
	walk(body)
	return node
}

func emitsBare(block []ir.Stmt, event string) bool {
	found := false
	_ = ir.Walk(block, func(n ir.Node) error {
		if e, ok := n.(*ir.Emit); ok && e.Name == event && len(e.Args) == 0 {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
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

// NameRecordPresses answers a test's pick of a record a data slot of
// callsite's `#id` holds -- `c.main.menu[1].children[2].select()`, an entry of
// a window's menu, noted in pkg.RecordPicks -- where body, the component's
// body before its props are substituted, draws a node per record under loops
// over the slot and its records' own slots, whose handler fires the record's
// event. The node is given an `#id` of its own, `<id>__<slot>`, a slot more per
// level (`main__menu__children`), and the pick becomes a press of its copy at
// the record's indexes. So the handler the target drew runs, as a user's pick
// does, with the updates it carries; the record's own handler, which the pick
// runs where the target draws nothing for it, carries none on a compiled
// target.
//
// It runs where NamePresses does, ahead of the props: a loop over the slot is
// still a loop over the slot's prop there, rather than over the records the
// call site wrote.
func NameRecordPresses(pkg *ir.Package, body []ir.Stmt, callsite *ir.NodeInst) (commit func()) {
	commit = func() {}
	if pkg == nil || callsite == nil || callsite.ID == "" || len(pkg.RecordPicks) == 0 {
		return commit
	}
	type named struct {
		id, fires string
		node      *ir.NodeInst
	}
	found := map[string]named{}
	var mine []*ir.Call
	for c, p := range pkg.RecordPicks {
		if p.Root.Field != callsite.ID || !sameComponent(p.Root.Type, callsite.Component) {
			continue
		}
		key := strings.Join(p.Slots, "__") + "@" + p.Event
		if _, done := found[key]; !done {
			n, fires := recordNode(body, p.Slots, p.Event)
			if n == nil {
				forwardPick(p, body, callsite)
				continue
			}
			id := callsite.ID + "__" + strings.Join(p.Slots, "__")
			if n.ID != id {
				n.ID = id
				n.Handle = &ir.Var{Name: id, Type: n.Component.SymType(), IsConst: true, NodeHandle: true}
			}
			found[key] = named{id: id, fires: fires, node: n}
		}
		mine = append(mine, c)
	}
	return func() {
		for _, c := range mine {
			p := pkg.RecordPicks[c]
			nm := found[strings.Join(p.Slots, "__")+"@"+p.Event]
			t := nm.node.Component.SymType()
			lists := t
			for range p.Idx {
				lists = ir.ListOf(lists)
			}
			// A node reached through an instance is the test's own instance's
			// node, as passTestReach flattens every other: `c.app.pokedex`
			// presses `c.pokedex__menu`.
			sel := *p.Root
			sel.Operand = testInstanceOf(p.Root.Operand)
			sel.Field = nm.id
			sel.Type = lists
			sel.Ref = composeRef(p.Root.Ref, ir.RefCount(strings.Repeat(string(ir.RefList), len(p.Idx))+string(ir.RefOne)))
			var r ir.Expr = &sel
			for _, ix := range p.Idx {
				lists = lists.Elems[0]
				r = &ir.Index{Operand: r, Idx: ix, Type: lists}
			}
			c.Callee, c.Receiver, c.Event, c.Args = nil, r, nm.fires, nil
			delete(pkg.RecordPicks, c)
		}
	}
}

// forwardPick follows a pick into the node body hands its records on to --
// html's window writes `ui.menubar(items=menu)`, whose body draws the entries
// -- by naming the node and picking from that node's slot instead, so the
// press is named where its body is spliced.
func forwardPick(p *ir.RecordPick, body []ir.Stmt, callsite *ir.NodeInst) {
	var fwd *ir.NodeInst
	var slot string
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if fwd != nil {
				return
			}
			if _, loop := s.(*ir.For); loop {
				continue
			}
			if n, ok := s.(*ir.NodeInst); ok && n.Component != nil {
				for _, a := range n.Props {
					if id, ok := a.Value.(*ir.Ident); ok && id.Name == p.Slots[0] {
						if _, isParam := id.Sym.(*ir.Param); isParam {
							fwd, slot = n, a.Name
							return
						}
					}
				}
			}
			for _, b := range ir.ViewBlocks(s) {
				walk(*b)
			}
		}
	}
	walk(body)
	if fwd == nil {
		return
	}
	if fwd.ID == "" {
		fwd.ID = callsite.ID + "__" + p.Slots[0]
		fwd.Handle = &ir.Var{Name: fwd.ID, Type: fwd.Component.SymType(), IsConst: true, NodeHandle: true}
	}
	root := *p.Root
	root.Field, root.Type = fwd.ID, fwd.Component.SymType()
	p.Root = &root
	p.Slots = append([]string{slot}, p.Slots[1:]...)
}

// testInstanceOf is the test's own instance a node ref through instances
// starts from.
func testInstanceOf(e ir.Expr) ir.Expr {
	for {
		sel, ok := e.(*ir.Select)
		if !ok {
			return e
		}
		e = sel.Operand
	}
}

// recordPath reads a trigger's receiver as a record reached through data
// slots: the select naming the node, the slot read at each level and the
// index into it. It returns a nil root for any other receiver.
func recordPath(r ir.Expr) (root *ir.Select, slots []string, idx []ir.Expr) {
	for {
		if cv, ok := r.(*ir.Conversion); ok {
			r = cv.Operand
			continue
		}
		ix, ok := r.(*ir.Index)
		if !ok {
			break
		}
		sel, ok := ix.Operand.(*ir.Select)
		if !ok {
			return nil, nil, nil
		}
		slots = append([]string{sel.Field}, slots...)
		idx = append([]ir.Expr{ix.Idx}, idx...)
		r = sel.Operand
	}
	for {
		cv, ok := r.(*ir.Conversion)
		if !ok {
			break
		}
		r = cv.Operand
	}
	sel, ok := r.(*ir.Select)
	if !ok || len(slots) == 0 {
		return nil, nil, nil
	}
	return sel, slots, idx
}

// recordNode is the node body draws per record at the end of slots -- under an
// indexed loop over the slot's prop, and one over each record's slot below it
// -- whose handler fires event on the innermost loop's record, and the
// handler's name.
func recordNode(body []ir.Stmt, slots []string, event string) (*ir.NodeInst, string) {
	var node *ir.NodeInst
	var fires string
	var walk func(stmts []ir.Stmt, loops []*ir.For)
	walk = func(stmts []ir.Stmt, loops []*ir.For) {
		for _, s := range stmts {
			if node != nil {
				return
			}
			if f, ok := s.(*ir.For); ok && len(loops) < len(slots) && overSlot(f, loops, slots[len(loops)]) {
				walk(f.Body, append(slices.Clip(loops), f))
				continue
			}
			if x, ok := s.(*ir.NodeInst); ok && len(loops) == len(slots) {
				if h := firesRecordEvent(x, loops[len(loops)-1], event); h != "" {
					node, fires = x, h
					return
				}
			}
			for _, b := range ir.ViewBlocks(s) {
				walk(*b, loops)
			}
		}
	}
	walk(body, nil)
	return node, fires
}

// overSlot reports whether f walks slot with an index: the component's own
// slot at the top, and the slot of the record the loop around it binds below.
func overSlot(f *ir.For, loops []*ir.For, slot string) bool {
	if ir.DeriveIterKind(f) != ir.IterIndexed || f.KeySym == nil || f.ValueSym == nil {
		return false
	}
	switch it := f.Iter.(type) {
	case *ir.Ident:
		_, isParam := it.Sym.(*ir.Param)
		return len(loops) == 0 && isParam && it.Name == slot
	case *ir.Select:
		id, ok := it.Operand.(*ir.Ident)
		return ok && len(loops) > 0 && it.Field == slot && id.Sym == ir.Symbol(loops[len(loops)-1].ValueSym)
	}
	return false
}

// firesRecordEvent is the name of n's handler that calls event on the record
// f binds, or "click" where a callback taking nothing does -- android's menu
// entry is a composable handed an `onClick` -- since that is what calls one;
// or "".
func firesRecordEvent(n *ir.NodeInst, f *ir.For, event string) string {
	for _, h := range n.Handlers {
		if h.Func != nil && callsRecordEvent(h.Func.Block, f, event) {
			return h.Name
		}
	}
	for _, p := range n.Props {
		if l, ok := p.Value.(*ir.Lambda); ok && l.Func != nil && len(l.Func.Params) == 0 && callsRecordEvent(l.Func.Block, f, event) {
			return "click"
		}
	}
	return ""
}

// callsRecordEvent reports whether block calls event on the record f binds.
func callsRecordEvent(block []ir.Stmt, f *ir.For, event string) bool {
	fires := false
	_ = ir.Walk(block, func(x ir.Node) error {
		c, ok := x.(*ir.Call)
		if !ok {
			return nil
		}
		sel, ok := c.Callee.(*ir.Select)
		if !ok || sel.Field != event {
			return nil
		}
		if id, ok := sel.Operand.(*ir.Ident); ok && id.Sym == ir.Symbol(f.ValueSym) {
			fires = true
		}
		return nil
	})
	return fires
}
