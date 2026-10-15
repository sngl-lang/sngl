package checker

import (
	"maps"
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
	if slot == nil && c.funcDepth > 0 && x.Field == ir.SlotEmptyMember {
		// A handler or a function runs outside any render, where there is no
		// content to ask about; a name it binds itself is its own.
		if _, bound := c.scope.Lookup(id.Name); !bound {
			if s := ownerSlot(c.currentComponent, id.Name); s != nil && s.Values == nil {
				c.error(x.Pos, "%s.%s asks what the call site rendered, which only the view can ask", id.Name, ir.SlotEmptyMember)
				return &ir.Literal{Type: TypBool, Value: "false"}
			}
		}
	}
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
// nothing, or whose head cannot be measured, has none. That includes the
// component entries a population supplied here forwards into a slot asked,
// whose content the component writes beside the insertion. Deferred, since
// the component's body may be checked after the call.
func (c *checker) checkSlotEmptyAnswers(node *ir.NodeInst) {
	comp := node.Component
	if comp == nil {
		return
	}
	if len(c.slotsAskedEmpty(comp)) == 0 && !suppliesEntries(node) {
		return
	}
	asked, entries := c.askedAt(node, c.slotsAskedEmpty)
	cannot := func(name string) {
		c.error(ir.NodePos(node), "%s asks whether its %s renders anything, and what this supplies for it is a loop that cannot say: one whose copies may each render nothing, or whose head is not a list, a map or a counted sequence", comp.DisplayName(), name)
	}
	for _, slot := range comp.Slots {
		if !asked[slot] {
			continue
		}
		body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: slot.Name, Rest: slot.Rest}, node)
		if !supplied {
			continue
		}
		if _, ok := ir.RendersNothing(body); !ok {
			cannot(slot.Name)
		}
	}
	for _, f := range entries {
		if !f.ok {
			c.error(ir.NodePos(node), "what this supplies forwards %s into a slot asked whether it renders anything, and %s populates %s with a loop that cannot say: one whose copies may each render nothing, or whose head is not a list, a map or a counted sequence", f.entry.Name, comp.DisplayName(), f.entry.Name)
		}
	}
}

// suppliesEntries is whether node populates a slot of its component that
// declares component entries.
func suppliesEntries(node *ir.NodeInst) bool {
	for _, s := range node.Component.Slots {
		if len(s.Slots) > 0 && node.Slots[s.Name] != nil {
			return true
		}
	}
	return false
}

// slotsAskedEmpty is the slots comp's body asks `.empty` of, directly or by
// forwarding the slot into one a component it renders asks of: the forwarded
// content is what that question is answered from, so it has to have an
// answer as much as content handed in directly. Every body a target may
// render for comp is read, its overrides' included, since a program is
// checked without knowing which target a build picks.
//
// A recursion asks what each of its members asks, so the answer is a fixed
// point over every component comp reaches, and each of those is settled with
// it: what a component asks turns only on what it renders.
func (c *checker) slotsAskedEmpty(comp *ir.Component) map[*ir.SlotDecl]bool {
	if out, ok := c.askedEmpty[comp]; ok {
		return out
	}
	if c.askedEmpty == nil {
		c.askedEmpty = map[*ir.Component]map[*ir.SlotDecl]bool{}
	}
	var reached []*ir.Component
	sets := map[*ir.Component]map[*ir.SlotDecl]bool{}
	var reach func(*ir.Component)
	reach = func(k *ir.Component) {
		if _, done := c.askedEmpty[k]; done {
			return
		}
		if _, seen := sets[k]; seen {
			return
		}
		sets[k] = map[*ir.SlotDecl]bool{}
		reached = append(reached, k)
		for _, body := range c.renderedBodies(k) {
			ir.WalkView(body, func(s ir.Stmt) bool {
				if n, ok := s.(*ir.NodeInst); ok && n.Component != nil {
					reach(n.Component)
				}
				return true
			})
		}
	}
	reach(comp)
	lookup := func(k *ir.Component) map[*ir.SlotDecl]bool {
		if out, ok := c.askedEmpty[k]; ok {
			return out
		}
		return sets[k]
	}
	for changed := true; changed; {
		changed = false
		for _, k := range reached {
			out := sets[k]
			mark := func(e ir.Expr) {
				_ = ir.WalkExprs(e, func(e ir.Expr) error {
					if se, ok := e.(*ir.SlotEmpty); ok && slices.Contains(k.Slots, se.Slot) && !out[se.Slot] {
						out[se.Slot] = true
						changed = true
					}
					return nil
				})
			}
			for _, body := range c.renderedBodies(k) {
				_ = ir.WalkExprs(body, func(e ir.Expr) error {
					mark(e)
					return nil
				})
				ir.WalkView(body, func(s ir.Stmt) bool {
					node, ok := s.(*ir.NodeInst)
					if !ok || node.Component == nil {
						return true
					}
					for _, a := range c.answersAsked(node, lookup) {
						mark(a)
					}
					return true
				})
			}
		}
	}
	for _, k := range reached {
		c.askedEmpty[k] = sets[k]
	}
	return sets[comp]
}

// renderedBodies is every body a target may render for comp: its own and
// each override's.
func (c *checker) renderedBodies(comp *ir.Component) [][]ir.Stmt {
	out := [][]ir.Stmt{comp.Stmts}
	all := c.overridesOf(comp)
	for _, m := range []map[string]ir.Override{all.Platform, all.Language} {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			out = append(out, m[k].Stmts)
		}
	}
	return out
}

// answersAsked is the answer to each question node's component asks of what
// node supplies it, given asked, what that component asks: the content of
// each slot asked, and of each component entry a population node supplies
// forwards into one. A forward in an answer asks the slot it forwards.
func (c *checker) answersAsked(node *ir.NodeInst, lookup func(*ir.Component) map[*ir.SlotDecl]bool) []ir.Expr {
	asked, entries := c.askedAt(node, lookup)
	var out []ir.Expr
	for _, slot := range node.Component.Slots {
		if !asked[slot] {
			continue
		}
		body, _, supplied := ir.SlotBody(&ir.SlotInst{Name: slot.Name, Rest: slot.Rest}, node)
		if !supplied {
			continue
		}
		if answer, ok := ir.RendersNothing(body); ok {
			out = append(out, answer)
		}
	}
	for _, f := range entries {
		if f.ok {
			out = append(out, f.answer)
		}
	}
	return out
}

// askedAt is the slots of node's component whose content node supplies is
// asked `.empty`: what the component asks (lookup), and each slot the content
// of a forwarded entry forwards in turn, since that content is written in the
// component's body and its slots answer to node.
func (c *checker) askedAt(node *ir.NodeInst, lookup func(*ir.Component) map[*ir.SlotDecl]bool) (map[*ir.SlotDecl]bool, []forwardedEntry) {
	entries := c.forwardedEntries(node, lookup)
	asked := lookup(node.Component)
	for _, f := range entries {
		if !f.ok {
			continue
		}
		_ = ir.WalkExprs(f.answer, func(e ir.Expr) error {
			if se, ok := e.(*ir.SlotEmpty); ok && slices.Contains(node.Component.Slots, se.Slot) && !asked[se.Slot] {
				asked = maps.Clone(asked)
				if asked == nil {
					asked = map[*ir.SlotDecl]bool{}
				}
				asked[se.Slot] = true
			}
			return nil
		})
	}
	return asked, entries
}

// forwardedEntry is a component entry a population forwards into a slot that
// is asked `.empty`, and what the component inserting the population
// populated the entry with, where it is written beside the insertion.
type forwardedEntry struct {
	entry  *ir.SlotDecl
	answer ir.Expr
	ok     bool
}

// forwardedEntries is, for each slot of node's component a population
// node supplies populates with entries, each entry the population forwards
// into an asked slot, answered by every insertion of the slot in the
// component's bodies that populates it.
func (c *checker) forwardedEntries(node *ir.NodeInst, lookup func(*ir.Component) map[*ir.SlotDecl]bool) []forwardedEntry {
	var out []forwardedEntry
	for _, slot := range node.Component.Slots {
		pop := node.Slots[slot.Name]
		if len(slot.Slots) == 0 || pop == nil {
			continue
		}
		entries := map[*ir.SlotDecl]bool{}
		ir.WalkView(pop.Body, func(s ir.Stmt) bool {
			inner, ok := s.(*ir.NodeInst)
			if !ok || inner.Component == nil {
				return true
			}
			for _, a := range c.answersAsked(inner, lookup) {
				_ = ir.WalkExprs(a, func(e ir.Expr) error {
					if se, ok := e.(*ir.SlotEmpty); ok && slices.Contains(slot.Slots, se.Slot) {
						entries[se.Slot] = true
					}
					return nil
				})
			}
			return true
		})
		for _, entry := range slot.Slots {
			if !entries[entry] {
				continue
			}
			for _, body := range c.renderedBodies(node.Component) {
				ir.WalkView(body, func(s ir.Stmt) bool {
					ins, ok := s.(*ir.SlotInst)
					if !ok || ins.Decl != slot {
						return true
					}
					content := ins.Slots[entry.Name]
					if content == nil {
						return true
					}
					answer, ok := ir.RendersNothing(content.Body)
					out = append(out, forwardedEntry{entry: entry, answer: answer, ok: ok})
					return true
				})
			}
		}
	}
	return out
}
