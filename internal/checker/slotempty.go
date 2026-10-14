package checker

import (
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// inferSlotEmpty is `src.empty` in a component's view: whether what the call
// site supplied for the slot renders nothing (ir.SlotEmpty). A slot is no
// value, so its name is read through only to ask that; a data slot's name is
// the list of its records, which answers to its own members.
func (c *checker) inferSlotEmpty(x *ast.SelectExpr) ir.Expr {
	id, ok := x.Operand.(*ast.IdentExpr)
	if !ok {
		return nil
	}
	slot := c.enclosingSlot(id.Name)
	if slot == nil || slot.Values != nil {
		return nil
	}
	if x.Field != ir.SlotEmptyMember {
		c.error(x.Pos, "%s is a slot, which is not a value: %s.%s asks whether what the call site supplied for it renders nothing", id.Name, id.Name, ir.SlotEmptyMember)
		return &ir.Literal{Type: TypBool, Value: "false"}
	}
	// What a scoped population renders may turn on the arguments an
	// insertion hands it, and asking the slot hands it none.
	if len(slot.Params) > 0 {
		c.error(x.Pos, "%s.%s: %s is handed arguments where it is inserted, and what it renders may turn on them", id.Name, ir.SlotEmptyMember, id.Name)
		return &ir.Literal{Type: TypBool, Value: "false"}
	}
	return &ir.SlotEmpty{AST: x, Slot: slot}
}

// checkSlotEmptyAnswers holds what a call site supplied for each slot its
// component asks `.empty` of to content the question has an answer for where
// it is written (ir.RendersNothing): a loop whose copies may each render
// nothing, or whose head cannot be measured, has none. Deferred, since the
// component's body may be checked after the call.
func (c *checker) checkSlotEmptyAnswers(node *ir.NodeInst) {
	comp := node.Component
	if comp == nil {
		return
	}
	asked := c.slotsAskedEmpty(comp)
	if len(asked) == 0 {
		return
	}
	for _, slot := range comp.Slots {
		if !asked[slot.Name] {
			continue
		}
		body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: slot.Name, Rest: slot.Rest}, node)
		if !supplied {
			continue
		}
		if _, ok := ir.RendersNothing(body); !ok {
			c.error(ir.NodePos(node), "%s asks whether its %s renders anything, and what this supplies for it is a loop that cannot say: one whose copies may each render nothing, or whose head is not a list, a map or a counted sequence", comp.DisplayName(), slot.Name)
		}
	}
}

// slotsAskedEmpty is the names of the slots comp's body asks `.empty` of,
// directly or by forwarding the slot into one a component it renders asks of:
// the forwarded content is what that question is answered from, so it has to
// have an answer as much as content handed in directly.
func (c *checker) slotsAskedEmpty(comp *ir.Component) map[string]bool {
	if out, ok := c.askedEmpty[comp]; ok {
		return out
	}
	if c.askedEmpty == nil {
		c.askedEmpty = map[*ir.Component]map[string]bool{}
	}
	out := map[string]bool{}
	c.askedEmpty[comp] = out // a recursion asks what it already found
	own := func(s *ir.SlotDecl) bool {
		return slices.Contains(comp.Slots, s)
	}
	_ = ir.WalkExprs(comp.Stmts, func(e ir.Expr) error {
		if se, ok := e.(*ir.SlotEmpty); ok && own(se.Slot) {
			out[se.Slot.Name] = true
		}
		return nil
	})
	ir.WalkView(comp.Stmts, func(s ir.Stmt) bool {
		node, ok := s.(*ir.NodeInst)
		if !ok || node.Component == nil || node.Component == comp {
			return true
		}
		asked := c.slotsAskedEmpty(node.Component)
		for _, slot := range node.Component.Slots {
			if !asked[slot.Name] {
				continue
			}
			body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: slot.Name, Rest: slot.Rest}, node)
			if !supplied {
				continue
			}
			answer, ok := ir.RendersNothing(body)
			if !ok {
				continue
			}
			_ = ir.WalkExprs(answer, func(e ir.Expr) error {
				if se, ok := e.(*ir.SlotEmpty); ok && own(se.Slot) {
					out[se.Slot.Name] = true
				}
				return nil
			})
		}
		return true
	})
	return out
}
