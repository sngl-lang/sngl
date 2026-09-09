package checker

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// entryOption is the build directive's prop naming the window a build opens
// at. It is declared on `output` itself and not on a language or a platform:
// which window is the program is a property of the program.
const entryOption = "entry"

// resolveEntryWindow binds what `output(entry = home)` named to a window.
//
// This is the fact codegen refused to guess: it scopes a build to the single
// window a program has and, past one, to nothing at all, deliberately, because
// nothing said which.
//
// The value is an element reference and not a string, so a name nothing
// declares is an unresolved name where it is written, reported by the
// expression check that failed to resolve it. What is left to report here is a
// name that *did* resolve, to something other than a window.
func (c *checker) resolveEntryWindow(root *ir.NodeInst) {
	if root == nil {
		return
	}
	for _, p := range root.Props {
		if p.Name != entryOption {
			continue
		}
		id, ok := p.Value.(*ir.Ident)
		if !ok {
			c.error(p.NamePos, "output %s names a window by its #id, not a value", entryOption)
			return
		}
		if w, isWindow := id.Sym.(*ir.Window); isWindow {
			c.pkg.EntryWindow = w.Name
			return
		}
		// A window a component renders is bound in that component's scope
		// rather than the root one, so the reference resolves to nothing there
		// and the name is what finds it.
		for _, w := range c.packageWindows() {
			if w.Name == id.Name {
				c.pkg.EntryWindow = w.Name
				return
			}
		}
		if id.Sym != nil {
			c.error(p.NamePos, "output %s names a window, and %s is not one", entryOption, id.Name)
		}
		return
	}
}

// packageWindows is every window the program declares: those at the root of a
// file and those a component body renders.
func (c *checker) packageWindows() []*ir.Window {
	out := append([]*ir.Window{}, c.pkg.Windows...)
	collect := func(stmts []ir.Stmt) {
		ir.WalkStmts(stmts, func(s ir.Stmt) error {
			if w, ok := s.(*ir.Window); ok {
				out = append(out, w)
				return ir.SkipDir
			}
			return nil
		})
	}
	collect(c.pkg.Body)
	for _, comp := range c.pkg.Components {
		collect(comp.Body)
	}
	return out
}
