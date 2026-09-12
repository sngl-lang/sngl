package lower

import (
	"fmt"
	"slices"

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
// A *component's* is a different question, and the answer is hoistRootState
// below: it has no pkg.Vars to have been left in, and the component it was
// declared on is an empty shell once its windows are lifted out of it (#215).
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
		if err := hoistRootState(comp, own); err != nil {
			return err
		}
		lifted = append(lifted, own...)
		comp.Body = nil
	}
	pkg.Windows = append(lifted, pkg.Windows...)
	return nil
}

// hoistRootState moves the vars, funcs and timers a root component declared
// into the window it lifts, because that is where they are mounted: the
// component is an empty shell by the end of this pass, and a declaration left
// on one reaches no backend -- `component main root { var n = 0; window … }`
// emitted `var state = {}` and rendered nothing for `n`.
//
// A window is a state owner every consumer already reads (ir.Owners,
// ModelState, ModelFreeFuncs, fyneComponentFuncs), and its funcs are already
// Model methods, so nothing downstream grows a case. The *ir.Var and *ir.Func
// pointers move rather than being copied, which is what keeps the references
// the body already holds pointing at them.
//
// Several windows is refused rather than guessed. One declaration mounted in
// two places is two answers: aliasing the pointer emits the field twice, and
// copying it turns one binding into two cells that diverge silently -- and
// which of those is wrong depends on the target, since two html windows are
// two pages with their own state while two fyne windows share one Model. So
// the author is asked, the way a body rendering two families is asked to name
// the one it belongs to. A component that declares nothing is unaffected,
// which is every multi-window root component in this repository.
func hoistRootState(comp *ir.Component, windows []*ir.Window) error {
	if comp == nil || (len(comp.Vars) == 0 && len(comp.Funcs) == 0 && len(comp.Timers) == 0) {
		return nil
	}
	// No window at all means nothing is mounted and nothing renders the
	// declarations either, so there is nothing to report and nowhere to move.
	if len(windows) == 0 {
		return nil
	}
	if len(windows) > 1 {
		return fmt.Errorf("%s: component %s declares state and renders %d windows, so there is no one window to mount it on; declare it in the window that uses it",
			rootStatePos(comp), comp.Name, len(windows))
	}
	w := windows[0]
	w.Vars = append(w.Vars, comp.Vars...)
	w.Funcs = append(w.Funcs, comp.Funcs...)
	w.Timers = append(w.Timers, comp.Timers...)
	dropComponentReceiver(w, comp)
	comp.Vars, comp.Funcs, comp.Timers = nil, nil, nil
	return nil
}

// dropComponentReceiver makes the moved funcs window funcs rather than methods
// of a component that no longer exists: the receiver param goes, and so does
// the receiver at every call site that named it.
//
// A component-body `func` is a method (`Receiver == comp.Name`, an implicit
// `this`), and a window-body one is an ordinary func -- the two spellings a
// backend already distinguishes. Left as a method, `biggest()` came out of the
// html route emitter as a free `MainBiggest(s)` beside the `s.Biggest()` method
// it had also emitted, because the receiver still named the shell. The body
// needs no rewriting: it reaches the owner's state through the emitter's scope
// and never through `this`.
func dropComponentReceiver(w *ir.Window, comp *ir.Component) {
	moved := make(map[*ir.Func]bool, len(comp.Funcs))
	for _, fn := range comp.Funcs {
		if fn == nil || fn.Receiver != comp.Name {
			continue
		}
		moved[fn] = true
		fn.Receiver = ""
		fn.RecvParam = nil
		// ir.Param.Receiver is the flag rather than the name: the checker sets
		// it on exactly the synthetic parameter it prepended.
		fn.Params = slices.DeleteFunc(slices.Clone(fn.Params), func(p *ir.Param) bool {
			return p != nil && p.Receiver
		})
	}
	if len(moved) == 0 {
		return
	}
	_ = ir.Walk(w, func(n ir.Node) error {
		if call, ok := n.(*ir.Call); ok && moved[call.Func] {
			call.Receiver = nil
		}
		return nil
	})
}

// rootStatePos points at the first declaration that has nowhere to go, which
// is a better place than the component header for an author to look.
func rootStatePos(comp *ir.Component) string {
	for _, v := range comp.Vars {
		if v.AST == nil {
			continue
		}
		if p := v.AST.StmtPos(); p != nil && p.IsValid() {
			return p.String()
		}
	}
	for _, fn := range comp.Funcs {
		if fn.AST != nil && fn.AST.Pos.IsValid() {
			return fn.AST.Pos.String()
		}
	}
	if comp.AST != nil && comp.AST.Pos.IsValid() {
		return comp.AST.Pos.String()
	}
	return comp.Name
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
