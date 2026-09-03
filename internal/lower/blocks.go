package lower

import "git.duckfam.us/jonathan/sngl/ir"

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
// and for nothing else.
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
	if pkg == nil {
		return nil
	}
	c := &blockCollector{}
	for _, f := range pkg.Funcs {
		c.add(&f.Block)
	}
	for _, comp := range pkg.Components {
		c.owner(comp.Funcs, comp.Vars, comp.Timers, comp.Body)
	}
	for _, w := range pkg.Windows {
		c.owner(w.Funcs, w.Vars, nil, w.Body)
		if w.ErrorHandler != nil && w.ErrorHandler.Func != nil {
			c.add(&w.ErrorHandler.Func.Block)
		}
	}
	for _, t := range pkg.Timers {
		if t.Handler != nil {
			c.add(&t.Handler.Block)
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
			c.add(&l.Func.Block)
		}
		return nil
	})
	return c.out
}

type blockCollector struct{ out []*[]ir.Stmt }

func (c *blockCollector) add(b *[]ir.Stmt) { c.out = append(c.out, b) }

// owner covers one component's or window's imperative blocks: its own
// functions, the handlers on its vars and timers, and the handlers hanging off
// the nodes in its view.
func (c *blockCollector) owner(funcs []*ir.Func, vars []*ir.Var, timers []*ir.Timer, body []ir.Stmt) {
	for _, f := range funcs {
		c.add(&f.Block)
	}
	for _, v := range vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				c.add(&h.Func.Block)
			}
		}
	}
	for _, t := range timers {
		if t.Handler != nil {
			c.add(&t.Handler.Block)
		}
	}
	c.handlersIn(body)
}

// handlersIn walks a view body for the handler bodies it hosts.
func (c *blockCollector) handlersIn(stmts []ir.Stmt) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			for _, h := range n.Handlers {
				if h.Func != nil {
					c.add(&h.Func.Block)
				}
			}
			c.handlersIn(n.Children)
		case *ir.If:
			c.handlersIn(n.Body)
			c.handlersIn(n.Else)
		case *ir.For:
			c.handlersIn(n.Body)
			c.handlersIn(n.Else)
		case *ir.SlotInst:
			c.handlersIn(n.Children)
		case *ir.ErrorBoundary:
			// The boundary's own @error handler, which is a handler body like
			// any other -- ir.Walk reaches it and analyzeCaptures walks it, so
			// leaving it out here was an inconsistency rather than a rule.
			if n.Handler != nil && n.Handler.Func != nil {
				c.add(&n.Handler.Func.Block)
			}
			c.handlersIn(n.Children)
		case *ir.ContextProvider:
			c.handlersIn(n.Children)
		case *ir.Window:
			c.handlersIn(n.Body)
		}
	}
}
