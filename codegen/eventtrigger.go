package codegen

import "duckfam.us/sngl/ir"

// EventTrigger is what a test's event trigger -- `c.inc.click()`, a call the
// checker tagged with an Event -- addresses: the variable holding the instance
// under test, the `#id` of the node whose event it fires, and, for
// `c.row[1].change(true)` or `c.cell[1][0].click()`, which of the nodes loops
// rendered under that id it means. Read off the call's receiver as the checker
// resolved it, never off its source spelling: a node a slot population
// declares is written `c.card.inc` and resolves to `c.inc`, the node `c`
// renders, which is what every target's invoker is named for.
type EventTrigger struct {
	Recv, ID string
	// Path is the index into each loop around the node, outermost first, nil
	// for an id rendered once. A target's invoker for one is its `At` variant
	// (InvokerAt), handed the copy's key (Key) first.
	Path []ir.Expr
}

// Key is the copy the trigger names, as the node carries it in ir.CopyProp:
// nil for an id rendered once.
func (tt EventTrigger) Key() ir.Expr { return ir.CopyKey(tt.Path) }

// EventTriggerTarget reads the EventTrigger off c, or reports false for a
// call that is no trigger.
func EventTriggerTarget(c *ir.Call) (EventTrigger, bool) {
	recv, id, path, ok := ir.TriggerTarget(c)
	return EventTrigger{Recv: recv, ID: id, Path: path}, ok
}

// InvokerAt is the name of the invoker for one copy of a node loops rendered
// more than once, given the name of the invoker for the node: it takes the
// copy's key (ir.CopyKey) ahead of the event's arguments.
func InvokerAt(name string) string { return name + "At" }
