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
		// By handle first: a window's `#id` binds an *ir.Var like every other
		// node's, and the symbol is what tells two bodies each writing `#home`
		// apart. By name second, because a window a component renders is bound
		// in that component's scope rather than the root one, so the reference
		// resolves to nothing here and the name is all that is left.
		// A `for` over the windows binds its id to the whole run of them --
		// hoistForLoopWindowIDs declares a list<window> at package scope -- so
		// the name answers with as many windows as the loop has iterations and
		// an entry is one window. Refused here rather than resolved, because
		// EntryWindow matches by name and would otherwise hand a single-model
		// target whichever of them the unroll happened to put first.
		//
		// NodeHandle is what tells that binding from a node handle: both are
		// *ir.Var, and only the handle carries it.
		handle, _ := id.Sym.(*ir.Var)
		if handle != nil && !handle.NodeHandle {
			if handle.Type != nil && handle.Type.Kind == ir.TypeList {
				c.error(p.NamePos, "output %s names %s, which a loop declares once per iteration -- an entry is a single window, so name one declared outside the loop", entryOption, id.Name)
				return
			}
			handle = nil
		}
		for _, w := range c.packageWindows() {
			if (handle != nil && w.Handle == handle) || (handle == nil && w.ID == id.Name) {
				c.pkg.EntryWindow = w.ID
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
// file and those a body renders. ir.AllWindows is the one enumeration of that,
// shared with the lowering and with every platform, so the two answers to
// "which windows are there" cannot drift apart.
func (c *checker) packageWindows() []*ir.Window {
	return ir.AllWindows(c.pkg)
}
