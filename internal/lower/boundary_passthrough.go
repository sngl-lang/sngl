package lower

import "duckfam.us/sngl/ir"

// passBoundaryPassthrough splices each boundary that holds no fallback into
// what it holds, on every target, so no backend is handed one: gtk4 and fyne
// have no error-boundary emitter, and every other rendered its children
// inline, each having had to look through it where it asked what a node
// holds. By now every
// raise under a boundary is resolved to its handler (Call.ResolvedHandler,
// and the catch blocks passErrorCatch made), the handler has had its updaters
// injected and its loops stamped, and a fallback was lowered to an `if` by
// passBoundaryFailed: the boundary holds nothing of its own. Every window's
// content is under one, the boundary its @error is in each override.
//
// Last, because a boundary is how every pass before it reaches the handler:
// spliced away earlier, a handler survived only as the catch block's alias,
// which no walk follows, and a loop in it was never given its kind.
var passBoundaryPassthrough = pass{
	name:    "BoundaryPassthrough",
	enabled: func(Features) bool { return true },
	apply:   lowerBoundaryPassthrough,
}

func lowerBoundaryPassthrough(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	splice := func(stmts []ir.Stmt) []ir.Stmt { return spliceBoundaries(stmts) }
	pkg.Body = splice(pkg.Body)
	for _, c := range pkg.Components {
		c.Body = splice(c.Body)
	}
	funcs := map[*ir.Func]bool{}
	for _, f := range pkg.Funcs {
		funcs[f] = true
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			funcs[f] = true
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			for i := range x.Handlers {
				if f := x.Handlers[i].Func; f != nil {
					funcs[f] = true
				}
			}
		case *ir.Lambda:
			if x.Func != nil {
				funcs[x.Func] = true
			}
		}
		return nil
	})
	for f := range funcs {
		f.Block = splice(f.Block)
	}
	return nil
}

// spliceBoundaries is stmts with each fallback-less boundary replaced by its
// children, at any depth of the view.
func spliceBoundaries(stmts []ir.Stmt) []ir.Stmt {
	out := stmts[:0:0]
	for _, s := range stmts {
		if n, ok := s.(*ir.ErrorBoundary); ok && n.Handler != nil && len(n.Failed) == 0 {
			out = append(out, spliceBoundaries(n.Children)...)
			continue
		}
		// Every view block: a population, a fallback, a slot insertion's,
		// a provider's -- a boundary in any of them is one to splice.
		for _, b := range ir.ViewBlocks(s) {
			*b = spliceBoundaries(*b)
		}
		out = append(out, s)
	}
	return out
}
