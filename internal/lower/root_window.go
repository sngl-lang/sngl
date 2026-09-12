package lower

import (
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
		hoistRootState(comp, own)
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
// Several windows each get it, and what that *means* is the platform's answer
// rather than the language's. The same *ir.Var is mounted on both, so a target
// whose windows are one process shares one cell (bubbletea, fyne and gtk4 put
// it in one Model) and a target whose windows are separate pages or screens
// copies it (html writes a `state` per page, android a `remember` per screen).
// That is what those targets *are*, and forcing either way round in the
// lowering would be the language overriding the platform it compiled to. The
// author picks a target knowing this; top-level state shared across windows is
// mainly a performance tool, not the default way to write an application.
//
// Aliasing one pointer into two owners is the thing to watch: a Model built
// from both windows must emit one field, not two. ir.Owners and every
// ModelState builder dedupe by the *ir.Var pointer, which is why the pointer is
// shared rather than the declaration copied per window.
func hoistRootState(comp *ir.Component, windows []*ir.Window) {
	if comp == nil || len(windows) == 0 {
		return
	}
	if len(comp.Vars) == 0 && len(comp.Funcs) == 0 && len(comp.Timers) == 0 {
		return
	}
	// Once, before the loop: stripping a receiver is a mutation of the shared
	// *ir.Func, so asking again for the second window finds nothing to strip
	// and leaves that window's call sites still naming the component.
	moved := stripComponentReceivers(comp)
	for _, w := range windows {
		w.Vars = append(w.Vars, comp.Vars...)
		w.Funcs = append(w.Funcs, comp.Funcs...)
		w.Timers = append(w.Timers, comp.Timers...)
		clearCallReceivers(w, moved)
	}
	comp.Vars, comp.Funcs, comp.Timers = nil, nil, nil
}

// stripComponentReceivers makes the moved funcs window funcs rather than
// methods of a component that no longer exists, and reports which they were.
//
// A component-body `func` is a method (`Receiver == comp.Name`, an implicit
// `this`), and a window-body one is an ordinary func -- the two spellings a
// backend already distinguishes. Left as a method, `biggest()` came out of the
// html route emitter as a free `MainBiggest(s)` beside the `s.Biggest()` method
// it had also emitted, because the receiver still named the shell. The body
// needs no rewriting: it reaches the owner's state through the emitter's scope
// and never through `this`.
func stripComponentReceivers(comp *ir.Component) map[*ir.Func]bool {
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
	return moved
}

// clearCallReceivers drops the receiver at every call site in w that named one
// of the funcs stripComponentReceivers just made receiverless.
func clearCallReceivers(w *ir.Window, moved map[*ir.Func]bool) {
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
