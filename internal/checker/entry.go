package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// entryOption is the build directive's name for the window a build opens at.
// It is a global option and not a per-target one: which window is the program
// is a property of the program.
const entryOption = "entry"

// readEntryOption records `output(entry = home)`. The value is an element
// reference and not a string, so what a program writes there is a name the
// compiler resolves -- see resolveEntryWindow, which does the resolving once
// the windows exist.
func (c *checker) readEntryOption(vn *ast.VisualNode) {
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name != entryOption || arg.Value == nil {
			continue
		}
		id, isIdent := arg.Value.(*ast.IdentExpr)
		if !isIdent {
			c.error(vn.Pos, "output %s names a window by its #id, not a value", entryOption)
			continue
		}
		c.entryName, c.entryPos = id.Name, vn.Pos
	}
}

// resolveEntryWindow binds what `entry` named to a window, after the bodies
// that declare them have been checked.
//
// This is the fact codegen refused to guess: it scopes a build to the single
// window a program has and, past one, to nothing at all, deliberately, because
// nothing said which. Naming it is what lets that stop.
func (c *checker) resolveEntryWindow() {
	if c.entryName == "" {
		return
	}
	for _, w := range c.packageWindows() {
		if w.Name == c.entryName {
			c.pkg.EntryWindow = w.Name
			return
		}
	}
	if _, ok := c.symtab.Root.Lookup(c.entryName); ok {
		c.error(c.entryPos, "output %s names a window, and %s is not one", entryOption, c.entryName)
		return
	}
	c.error(c.entryPos, "output %s: no window is declared with the id %q", entryOption, c.entryName)
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
