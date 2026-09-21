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
// A *component's* goes to the package, which is what this pass makes the
// container: the windows it renders are lifted onto pkg.Windows and the
// component is emptied, so the declarations are left on a shell nothing
// instantiates and reach no backend at all -- `var hits = 0` came out of every
// target as a bare `hits` the Model never declared (#215).
//
// It used to be hoisted *into* each window, which is the same relationship read
// backwards, and cost a per-platform answer about what N windows do with one
// declaration. One destination now: the package is one cell on the three
// single-process targets and one declaration html instantiates per page, which
// is the same divergence and is stated where it is decided rather than here.
//
// Known gap: a *statement* that is not a window is dropped with the body it was
// written in, on both paths. A `time.timer(…)` at the root of a file or of a
// root component reaches no backend, in silence. Every timer fixture puts one
// in a `node` component rendered inside a window, which is why nothing caught
// it; fixing it means answering what N windows do with one timer, which is the
// question hoistRootState settles for state and not for effects.
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
		hoistRootState(pkg, comp, own)
		lifted = append(lifted, own...)
		comp.Body = nil
	}
	pkg.Windows = append(lifted, pkg.Windows...)
	return nil
}

// hoistRootState moves the vars and funcs a root component declared into the
// package, because the package is the container of the windows it rendered
// once this pass has lifted them: the component is an empty shell by the end of
// it, and a declaration left on one reaches no backend.
//
// Pointers move rather than being copied, so the references the body already
// holds keep pointing at them.
//
// A timer is not moved, and there is nothing to move: a timer primitive is an
// ordinary node in comp.Body, and applyRootWindow drops the body -- see its
// own note.
func hoistRootState(pkg *ir.Package, comp *ir.Component, windows []*ir.Window) {
	if pkg == nil || comp == nil || len(windows) == 0 {
		return
	}
	if len(comp.Vars) == 0 && len(comp.Funcs) == 0 {
		return
	}
	moved := stripComponentReceivers(comp)
	// Appended only where the package does not hold them already. A
	// component-body `func` is registered in pkg.Funcs *as well* as on the
	// component (the checker's registerNestedMethods appends the one *ir.Func
	// to each), so appending unconditionally gives the package two entries
	// pointing at one declaration -- and every backend emits pkg.Funcs, so a
	// helper the inliner does not substitute away comes out twice.
	pkg.Vars = appendMissing(pkg.Vars, comp.Vars)
	pkg.Funcs = appendMissing(pkg.Funcs, comp.Funcs)
	for _, w := range windows {
		clearCallReceivers(w, moved)
	}
	comp.Vars, comp.Funcs = nil, nil
}

// appendMissing adds the entries of add that dst does not already hold,
// comparing by pointer: a declaration is its identity here, as everywhere else
// in the IR.
func appendMissing[T comparable](dst []T, add []T) []T {
	for _, v := range add {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

// stripComponentReceivers makes the moved funcs package funcs rather than
// methods of a component that no longer exists, and reports which they were.
//
// A component-body `func` is a method (`Receiver == comp.Name`, an implicit
// `this`), and a package-level one is an ordinary func -- the two spellings a
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
