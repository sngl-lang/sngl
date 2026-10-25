package ir

import "slices"

// BlockLoops is the loops that make copies of what block b of s holds, given
// loops, those around s: a `for`'s body is one more, and the content of a
// record in its container's loop over the records one fewer
// (RecordBranchOf).
func BlockLoops(s Stmt, b *[]Stmt, loops []*For) []*For {
	switch x := s.(type) {
	case *For:
		if b == &x.Body {
			return append(slices.Clip(loops), x)
		}
	case *If:
		if f := RecordBranchOf(x.Cond, loops); f != nil && b == &x.Body {
			return slices.DeleteFunc(slices.Clone(loops), func(l *For) bool { return l == f })
		}
	}
	return loops
}

// ReportsAtRoot reports whether the node a call site's `#id` lands on, the
// body's first, handles event itself: a select's control, which a trigger
// reaches directly. A trigger of such an event presses nothing.
func ReportsAtRoot(body []Stmt, event string) bool {
	for _, s := range body {
		if n, ok := s.(*NodeInst); ok {
			return slices.ContainsFunc(n.Handlers, func(h EventHandler) bool { return h.Name == event })
		}
	}
	return false
}

// PressedNode is what a test's trigger of event through the `#id` of a call
// site of a component whose body is body presses, where the node the id lands
// on does not report the event (ReportsAtRoot): the first node of body under
// exactly one loop whose handler, or callback taking nothing, emits event with
// that loop's index (EmittedIndex) -- a tab of a strip, whose click emits
// `change(k)`. It returns the node, the loop whose copy at the trigger's
// argument is pressed, and what pressing fires: the handler's event, or a
// click for a callback, which is what calls one.
//
// The index is the first variable of a two-variable loop over a list
// (IterIndexed): one index names one copy. An element, a map's key or a
// counted loop's count names a value, not a copy, and a node under two loops
// has a copy per pair of indexes, which one index cannot name. Both the
// compiled targets (lower.NamePresses) and the interpreter
// (interp.View.Choosing) press by this rule.
func PressedNode(body []Stmt, event string) (node *NodeInst, loop *For, fires string) {
	if ReportsAtRoot(body, event) {
		return nil, nil, ""
	}
	var walk func(stmts []Stmt, loops []*For)
	walk = func(stmts []Stmt, loops []*For) {
		for _, s := range stmts {
			if node != nil {
				return
			}
			if x, ok := s.(*NodeInst); ok && len(loops) == 1 && x.Component != nil {
				if f := loops[0]; DeriveIterKind(f) == IterIndexed && f.KeySym != nil {
					if fires = pressFires(x, event, f.KeySym); fires != "" {
						node, loop = x, f
						return
					}
				}
			}
			for _, b := range ViewBlocks(s) {
				walk(*b, BlockLoops(s, b, loops))
			}
		}
	}
	walk(body, nil)
	return node, loop, fires
}

// pressFires is what pressing n fires where a handler or a callback taking
// nothing emits event with index, or "".
func pressFires(n *NodeInst, event string, index *LoopVar) string {
	for _, h := range n.Handlers {
		if h.Func != nil && EmittedIndex(h.Func.Block, event) == index {
			return h.Name
		}
	}
	for _, p := range n.Props {
		if l, ok := p.Value.(*Lambda); ok && l.Func != nil && len(l.Func.Params) == 0 && EmittedIndex(l.Func.Block, event) == index {
			return "click"
		}
	}
	return ""
}
