package checker

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// inferComponentTrees settles the family of every declaration that named none,
// reading it off what the body renders. Two rounds, and the order is the
// point:
//
//  1. Settle whoever has complete evidence, repeatedly. A mixture is reported
//     here, where it is known to be the whole of the evidence rather than the
//     part that has arrived so far.
//  2. What is left is in a cycle, so waiting for it to settle is waiting
//     forever. Settle those from the evidence that did resolve.
func (c *checker) inferComponentTrees() {
	pending := make(map[*ir.Component]bool, len(c.inferTrees))
	for _, comp := range c.inferTrees {
		pending[comp] = true
	}
	for _, partial := range []bool{false, true} {
		for progress := true; progress; {
			progress = false
			for _, comp := range c.inferTrees {
				if !pending[comp] || !c.settleTree(comp, pending, partial) {
					continue
				}
				delete(pending, comp)
				progress = true
			}
		}
	}
	for _, comp := range c.inferTrees {
		if !pending[comp] {
			continue
		}
		// The wrapper is the shape this lands on most, and the mark is the
		// wrong answer for it: it renders its caller's nodes rather than none,
		// so what it wants is to be handed a family rather than to disclaim
		// one.
		if insertsSlot(comp.Body) {
			c.error(compDeclPos(comp),
				"component %s: the body renders only what a caller supplies, so name the tree it belongs to in the return position -- or a type parameter, for a wrapper whose family is whatever it was handed",
				comp.Name)
			continue
		}
		c.error(compDeclPos(comp),
			"component %s: the body names no tree, so name the tree it belongs to in the return position, or mark it #[tree.none]",
			comp.Name)
	}
	// A call site's specialization is a copy of the declaration, and one taken
	// before the fixed point ran holds the family the declaration had then.
	for spec, decl := range c.specOrigin {
		spec.Tree = decl.Tree
	}
	c.inferTrees, c.specOrigin = nil, nil
}

// settleTree gives comp the family its body renders, and reports whether the
// question is now answered -- an ambiguous body is answered too, with a
// diagnostic and no family.
//
// partial says whether evidence that is still pending may be ignored. It may
// once every component that can settle on complete evidence has, since what is
// left then is a cycle, and waiting on a cycle is waiting forever.
func (c *checker) settleTree(comp *ir.Component, pending map[*ir.Component]bool, partial bool) bool {
	found, unsettled := c.treeEvidence(comp.Body, pending, nil)
	if unsettled && !partial {
		return false
	}
	switch len(found) {
	case 0:
		return false
	case 1:
		comp.Tree = found[0]
		c.declPkg().NoteTreeKind(found[0])
		return true
	}
	c.error(compDeclPos(comp),
		"component %s: the body renders both %s and %s, so name the tree it belongs to in the return position",
		comp.Name, found[0].Name, found[1].Name)
	return true
}

// treeEvidence is the distinct families the statements render, in the order
// they are written, and whether any node among them belongs to a declaration
// whose own family is still pending.
//
// A window is evidence like any node, read off the declaration the mark bound:
// a body of windows is a member of the family the package body accepts, which
// is what lets `component pages() { window … }` say what it does without
// naming `root`.
//
// A slot insertion is no evidence: what a slot with no declared family accepts
// is the family of the component declaring it, so reading one would be reading
// the answer off the question. Its *fallback* is evidence, and the distinction
// is who wrote the nodes: the insertion stands for the caller's, the fallback
// is this component's own, rendered when the caller supplies none.
func (c *checker) treeEvidence(stmts []ir.Stmt, pending map[*ir.Component]bool, found []*ir.StructDef) ([]*ir.StructDef, bool) {
	var unsettled bool
	nested := func(blocks ...[]ir.Stmt) {
		for _, b := range blocks {
			var u bool
			found, u = c.treeEvidence(b, pending, found)
			unsettled = unsettled || u
		}
	}
	note := func(sd *ir.StructDef) {
		if sd != nil && !slices.Contains(found, sd) {
			found = append(found, sd)
		}
	}
	for _, st := range stmts {
		// An `if`, a `for`, a boundary and a context override put nothing in
		// the tree themselves, so the evidence is whatever is under them.
		if blocks, _, ok := treeTransparent(st); ok {
			nested(blocks...)
			continue
		}
		switch s := st.(type) {
		case *ir.SlotInst:
			nested(s.Children)
		case *ir.Window:
			if c.windowComp != nil {
				note(c.windowComp.Tree)
			}
		case *ir.NodeInst:
			switch {
			case s.Component == nil:
			case s.Component.Tree != nil:
				note(s.Component.Tree)
			case pending[s.Component]:
				unsettled = true
			}
		}
	}
	return found, unsettled
}

// insertsSlot reports whether the body renders a slot's content, which is the
// one thing treeEvidence deliberately does not read.
func insertsSlot(stmts []ir.Stmt) bool {
	for _, st := range stmts {
		if blocks, _, ok := treeTransparent(st); ok {
			if slices.ContainsFunc(blocks, insertsSlot) {
				return true
			}
			continue
		}
		if _, ok := st.(*ir.SlotInst); ok {
			return true
		}
	}
	return false
}

// deferTreeCheck holds a membership check back until inferComponentTrees has
// settled every family. Run where it is written, a check whose subject is a
// declaration that has not been read yet would compare against no family and
// pass in silence -- which is the same nothing an unrestricted position
// reports, so the hole would be quiet.
func (c *checker) deferTreeCheck(check func()) {
	c.treeChecks = append(c.treeChecks, check)
}

// runTreeChecks drains them.
func (c *checker) runTreeChecks() {
	checks := c.treeChecks
	c.treeChecks = nil
	for _, check := range checks {
		check()
	}
}
