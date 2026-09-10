package checker

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// inferComponentTrees settles the family of every declaration that named none,
// reading it off what the body renders.
//
// The evidence is the root of the body: what the component puts in the tree,
// not what those nodes host, which is the same reading childrenTree gives a
// slot's content. An `if`, a `for` and a boundary are how the nodes under them
// got there rather than nodes, so the walk reaches through all three.
//
// It is a fixed point rather than one ordered pass because the evidence may be
// another declaration whose own family is unsettled, and two declarations may
// be mutually recursive. Two rounds, in this order:
//
//  1. Settle whoever has complete evidence, repeatedly. A body with a mixture
//     is reported here, where the mixture is known to be the whole of it.
//  2. Whatever is left is in a cycle. Settle those from the evidence that did
//     resolve, so `a` renders a widget and a `b` that renders an `a` is one
//     too. What that still leaves is a cycle with no ground truth anywhere in
//     it, which is a body that names no tree.
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
		if pending[comp] {
			c.error(compDeclPos(comp),
				"component %s: the body names no tree, so name the tree it belongs to in the return position, or mark it #[tree.none]",
				comp.Name)
		}
	}
	// A call site's specialization is a copy of the declaration, and one taken
	// before the fixed point ran holds the family the declaration had then.
	for spec, decl := range c.specOrigin {
		spec.Tree = decl.Tree
	}
	c.inferTrees = nil
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
// the answer off the question.
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
		switch s := st.(type) {
		case *ir.If:
			nested(s.Body, s.Else)
		case *ir.For:
			nested(s.Body, s.Else)
		case *ir.ErrorBoundary:
			nested(s.Children, s.Failed)
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

// deferTreeCheck holds a membership check back until inferComponentTrees has
// settled every family. Run where it is written, a check whose subject is a
// declaration that has not been read yet would compare against no family and
// pass in silence -- which is the same nothing an unrestricted position
// reports, so the hole would be quiet.
func (c *checker) deferTreeCheck(check func()) {
	c.treeChecks = append(c.treeChecks, check)
}

// runTreeChecks drains them, in the order they were recorded.
func (c *checker) runTreeChecks() {
	checks := c.treeChecks
	c.treeChecks = nil
	for _, check := range checks {
		check()
	}
}
