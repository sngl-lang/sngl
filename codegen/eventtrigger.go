package codegen

import "duckfam.us/sngl/ir"

// EventTrigger is what a test's event trigger -- `c.inc.click()`, a call the
// checker tagged with an Event -- addresses: the variable holding the instance
// under test, the `#id` of the node whose event it fires, and, for
// `c.row[1].change(true)`, which of the nodes a loop rendered under that id it
// means. Read off the call's receiver as the checker resolved it, never off
// its source spelling: a node a slot population declares is written
// `c.card.inc` and resolves to `c.inc`, the node `c` renders, which is what
// every target's invoker is named for.
type EventTrigger struct {
	Recv, ID string
	// Index is the copy the trigger names, nil for an id rendered once. A
	// target's invoker for one is its `At` variant (InvokerAt), handed the
	// index first.
	Index ir.Expr
}

// EventTriggerTarget reads the EventTrigger off c, or reports false for a
// call that is no trigger.
func EventTriggerTarget(c *ir.Call) (EventTrigger, bool) {
	if c == nil || c.Event == "" {
		return EventTrigger{}, false
	}
	recv := c.Receiver
	// A receiver read after a null test is the narrowing of it.
	if conv, narrowed := recv.(*ir.Conversion); narrowed {
		recv = conv.Operand
	}
	var index ir.Expr
	if ix, ok := recv.(*ir.Index); ok {
		recv, index = ix.Operand, ix.Idx
	}
	sel, isSel := recv.(*ir.Select)
	if !isSel {
		return EventTrigger{}, false
	}
	base, isIdent := sel.Operand.(*ir.Ident)
	if !isIdent {
		return EventTrigger{}, false
	}
	return EventTrigger{Recv: base.Name, ID: sel.Field, Index: index}, true
}

// InvokerAt is the name of the invoker for one copy of a node a loop rendered
// more than once, given the name of the invoker for the node: it takes the
// copy's index, from 0 in render order, ahead of the event's arguments.
func InvokerAt(name string) string { return name + "At" }
