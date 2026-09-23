package lower

import (
	"maps"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// imperativeBlocks returns every imperative block in pkg, in a stable order:
// the bodies that run as a statement stream, as opposed to a view body, which
// describes a tree.
//
// The distinction is the one passCSE documents at length and passForElse
// repeats: a target with no host language can hold a statement in a function,
// a handler or a timer, and cannot hold one in a view body -- static html
// writes markup, and a temp declared beside a node is not something markup can
// express. A pass that introduces a statement asks for these blocks and gets
// no view body among them.
//
// A view body is still walked, for the handlers hanging off the nodes in it
// and for nothing else. A pass that may put a statement in a view body asks
// allBlocks instead.
//
// That view descent is hand-written where the rest of lowering uses ir.Walk,
// because a caller needs the *address* of a statement list, which no node
// visit hands back, and because the split above is no filter over that walk.
//
// Lambda bodies come last, and come from ir.Walk rather than from a second
// hand-written descent: a lambda can sit in any expression, and the base
// traversal already knows where every expression is. They overlap the blocks
// above -- a lambda written inside a function body is reachable both ways --
// so a caller must be idempotent over a block it has already rewritten. Both
// current callers are: a loop whose else has been desugared no longer has one,
// and a call already bound to a temp is no longer made twice.
//
// This exists as one function because it was written twice. The second copy
// was byte-identical to the first and both were wrong in the same way for a
// while: neither reached a handler body on android, whose primitives declare
// the callback as a prop, so the body is a lambda in the node's props and the
// node's Handlers slice is empty.
func imperativeBlocks(pkg *ir.Package) []*[]ir.Stmt {
	return collectBlocks(pkg, true, false)
}

// allBlocks is imperativeBlocks plus the view-body statement lists themselves.
// passEffect wants those: a settle call goes after the write that moved a
// bracket's key list, and a write can sit in a view body -- one there is a
// statement every backend already emits, since that is where the pass puts the
// program's first settle.
func allBlocks(pkg *ir.Package) []*[]ir.Stmt {
	return collectBlocks(pkg, true, true)
}

// viewBlocks is the complement of imperativeBlocks: view-body statement lists
// alone. A pass reaching both would desugar one for-else twice.
func viewBlocks(pkg *ir.Package) []*[]ir.Stmt {
	return collectBlocks(pkg, false, true)
}

func collectBlocks(pkg *ir.Package, imperative, views bool) []*[]ir.Stmt {
	if pkg == nil {
		return nil
	}
	c := &blockCollector{imperative: imperative, views: views, seen: map[*[]ir.Stmt]bool{}}
	for _, f := range pkg.Funcs {
		c.addImperative(&f.Block)
	}
	// The order is ir.Owners' now rather than this file's, and it is
	// observable: passCSE and passForElse name their temps __cseN/__ranN off
	// the position a block holds here. It also reaches one set of blocks this
	// file never listed -- the handlers on a *package* var, which a component's
	// and a window's had and the package's did not
	// (testdata/for_else_package_var_handler.txtar).
	for _, o := range ir.Owners(pkg) {
		c.owner(o)
	}
	if imperative {
		_ = ir.Walk(pkg, func(n ir.Node) error {
			if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
				c.add(&l.Func.Block)
			}
			return nil
		})
	}
	return c.out
}

// blockCollector gathers block pointers once each. The dedupe is what lets a
// caller that is not idempotent use these: a handler body reached both through
// its node and as a lambda in that node's props is one block, not two.
type blockCollector struct {
	out        []*[]ir.Stmt
	seen       map[*[]ir.Stmt]bool
	imperative bool
	views      bool
}

func (c *blockCollector) addImperative(b *[]ir.Stmt) {
	if !c.imperative {
		return
	}
	c.add(b)
}

func (c *blockCollector) add(b *[]ir.Stmt) {
	if b == nil || c.seen[b] {
		return
	}
	c.seen[b] = true
	c.out = append(c.out, b)
}

// owner covers one declaration's imperative blocks: its own functions, the
// handlers on its vars, its own @error, and the handlers hanging off the nodes
// in its view -- a timer's @tick among them, the timer primitive being an
// ordinary node.
func (c *blockCollector) owner(o ir.Owner) {
	for _, f := range o.Funcs {
		c.addImperative(&f.Block)
	}
	for _, v := range o.Vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				c.addImperative(&h.Func.Block)
			}
		}
	}
	c.viewIn(o.Body)
	// After the view body, not before: passCSE and passForElse number their
	// temps off this order, and a window's @error came last when this file
	// enumerated the owners itself.
	for _, h := range o.Handlers {
		if h.Func != nil {
			c.addImperative(&h.Func.Block)
		}
	}
}

// viewIn walks a view body for the handler bodies it hosts, and for the body's
// own statement lists when the caller asked for those too.
func (c *blockCollector) viewIn(stmts *[]ir.Stmt) {
	if stmts == nil {
		return
	}
	if c.views {
		c.add(stmts)
	}
	for _, s := range *stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			for _, h := range n.Handlers {
				if h.Func != nil {
					c.addImperative(&h.Func.Block)
				}
			}
			c.viewIn(&n.Children)
			// Slot content is a view body the caller wrote, so the handlers
			// on it are the caller's imperative blocks like any other. By
			// name because Slots is a map, and this order is what numbers a
			// temp passCSE binds.
			for _, name := range slices.Sorted(maps.Keys(n.Slots)) {
				if sc := n.Slots[name]; sc != nil {
					c.viewIn(&sc.Body)
				}
			}
		case *ir.If:
			c.viewIn(&n.Body)
			c.viewIn(&n.Else)
		case *ir.For:
			c.viewIn(&n.Body)
			c.viewIn(&n.Else)
		case *ir.SlotInst:
			c.viewIn(&n.Children)
		case *ir.ErrorBoundary:
			// The boundary's own @error handler, which is a handler body like
			// any other -- ir.Walk reaches it and analyzeCaptures walks it, so
			// leaving it out here was an inconsistency rather than a rule.
			if n.Handler != nil && n.Handler.Func != nil {
				c.addImperative(&n.Handler.Func.Block)
			}
			c.viewIn(&n.Children)
			c.viewIn(&n.Failed)
		case *ir.ContextProvider:
			c.viewIn(&n.Children)
		}
	}
}
