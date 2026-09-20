package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// passRootWindow lifts the windows a package body holds onto pkg.Windows.
//
// The body is a slot for the root tree, and a window is that tree's one
// renderable member -- so what a program writes at the top level is windows,
// possibly through a component of its own that renders them. Downstream there
// is one shape for a window and no second one for a body: two dozen lowering
// passes and five platforms already walk pkg.Windows, and every bug in #133
// and #135 came from a body being a second thing each of them had to know.
//
// It used to wrap the body *in* a window instead, because a top-level `vbox`
// was what a program rendered. That is now a checker error -- a `ui` node is
// not a member of the root tree -- so there is nothing left to wrap.
//
// A window inside a root-level `if` or `for` is lifted too. The branch is a
// build-time one by then: the optimizer has folded what it can, and a
// condition that survives says which windows a build contains rather than
// which one is open.
//
// A *package* body's state stays in pkg.Vars, where the checker put it and
// where every consumer already reads it: the window owns the body, the package
// owns the state, and ir.Owners reports both.
//
// A *component's* stays on the component, which is the container the windows
// it renders were written in: a window is a rendering root and owns nothing,
// so nothing moves. It used to be hoisted *into* each window, which is the
// same relationship read backwards.
//
// Known gap: a *statement* that is not a window is dropped with the body it was
// written in, on both paths. A `time.timer(…)` at the root of a file or of a
// root component reaches no backend, in silence. Every timer fixture puts one
// in a `node` component rendered inside a window, which is why nothing caught
// it; fixing it means answering what N windows do with one timer, which is the
// question hoistRootState's per-platform answer settles for state and not for
// effects.
var passRootWindow = pass{
	name:    "RootWindow",
	enabled: func(Caps) bool { return true },
	apply:   applyRootWindow,
}

func applyRootWindow(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	lifted := liftWindows(pkg.Body)
	pkg.Body = nil
	// A component that names the root family renders windows rather than
	// widgets, so its body is a second place they are written. Left there, a
	// backend walking the component as a view meets a window in the middle of
	// one -- which bubbletea panics on and the others mis-render.
	for _, comp := range pkg.Components {
		if !ir.IsAppRootTree(comp.Tree) {
			continue
		}
		own := liftWindows(comp.Body)
		lifted = append(lifted, own...)
		comp.Body = nil
	}
	pkg.Windows = append(lifted, pkg.Windows...)
	return nil
}

// liftWindows is every window a block holds, including those a build-time
// branch put there.
func liftWindows(stmts []ir.Stmt) []*ir.Window {
	var out []*ir.Window
	ir.WalkStmts(stmts, func(s ir.Stmt) error {
		if w, ok := s.(*ir.Window); ok {
			out = append(out, w)
			return ir.SkipDir
		}
		return nil
	})
	return out
}
