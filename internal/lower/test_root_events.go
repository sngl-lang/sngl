package lower

import (
	"slices"

	"duckfam.us/sngl/ir"
)

// passTestRootEvents drops each raise of an event of the component a test
// build is rooted at. The test takes the component with no call site, so
// nothing listens: a card raising `@open` when its button is clicked raised
// it through an `emit` no Go target declares, and the build failed, as the
// Pokédex's tests of its detail page did once they took the page itself
// rather than a wrapper binding `@open`. What the raise is handed is still
// worked out where an argument can do something, a call among them.
//
// After passInstanceEvents, which turns a raise of a component built at run
// time into a call of the handler it was handed, so what is left is the
// root's; before the passes that bind what a raise is handed to a temp, which
// a dropped raise would leave unread.
var passTestRootEvents = pass{
	name:    "TestRootEvents",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		root := rootComponent(pkg, opts)
		if pkg == nil || !opts.Tests || root == nil || len(root.Events) == 0 {
			return nil
		}
		ir.RewriteBlocks(pkg, func(stmts []ir.Stmt) []ir.Stmt {
			if !slices.ContainsFunc(stmts, func(s ir.Stmt) bool { return rootRaise(s, root) }) {
				return stmts
			}
			var out []ir.Stmt
			for _, s := range stmts {
				if !rootRaise(s, root) {
					out = append(out, s)
					continue
				}
				for _, a := range s.(*ir.Emit).Args {
					if c, ok := a.Value.(*ir.Call); ok {
						out = append(out, &ir.CallStmt{Call: c})
					}
				}
			}
			return out
		})
		return nil
	},
}

// rootRaise reports whether s raises one of root's events.
func rootRaise(s ir.Stmt, root *ir.Component) bool {
	e, ok := s.(*ir.Emit)
	return ok && slices.ContainsFunc(root.Events, func(d *ir.EventDecl) bool { return d.Name == e.Name })
}
