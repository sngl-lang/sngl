package codegen

import "duckfam.us/sngl/ir"

// EventTriggerTarget is what a test's event trigger -- `c.inc.click()`, a
// call the checker tagged with an Event -- addresses: the variable holding the
// instance under test, and the `#id` of the node whose event it fires. Read
// off the call's receiver as the checker resolved it, never off its source
// spelling: a node a slot population declares is written `c.card.inc` and
// resolves to `c.inc`, the node `c` renders, which is what every target's
// invoker is named for.
func EventTriggerTarget(c *ir.Call) (recv, id string, ok bool) {
	if c == nil || c.Event == "" {
		return "", "", false
	}
	sel, isSel := c.Receiver.(*ir.Select)
	if !isSel {
		return "", "", false
	}
	base, isIdent := sel.Operand.(*ir.Ident)
	if !isIdent {
		return "", "", false
	}
	return base.Name, sel.Field, true
}
