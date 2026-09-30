package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passWindowUnderIf refuses a window written under an `if` that reads state on
// a target that still renders `window` as the builtin it is marked: html, until
// Phase C2 moves its pages to `nav.page`. Such a window says it exists only
// while the condition holds, and html writes every window it finds once, as a
// document, so emitting it would be emitting it as if the `if` were not there.
//
// Where a platform overrides `ui.window` -- gtk4 and fyne with a Toplevel,
// bubbletea and android with a Screen -- the window is an ordinary node by now
// (composeOverriddenBuiltins) and the `if` is an ordinary conditional, so
// there is nothing here to refuse. A
// condition that reads no state is not a lifetime either: the optimizer has
// decided it, or it never changes.
//
// Only an `if` at the root of the package body: a window a component renders
// has been spliced there by now. Phase C3, which leaves no window in the
// compiler, deletes this with the rest.
var passWindowUnderIf = pass{
	name:    "WindowUnderIf",
	enabled: func(Features) bool { return true },
	apply:   refuseWindowsUnderIf,
}

func refuseWindowsUnderIf(pkg *ir.Package, _ Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	fx := &effectState{pkg: pkg, reactive: collectReactiveVars(pkg)}
	var walk func(stmts []ir.Stmt, reactive bool) error
	walk = func(stmts []ir.Stmt, reactive bool) error {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.If:
				r := reactive || len(fx.reactiveVarsIn(n.Cond)) > 0
				if err := walk(n.Body, r); err != nil {
					return err
				}
				if err := walk(n.Else, r); err != nil {
					return err
				}
			case *ir.NodeInst:
				if ir.IsWindowNode(n) && reactive {
					platform := opts.Platform
					if platform == "" {
						platform = "this target"
					}
					return fmt.Errorf("%s: a window under an `if` that reads state is created and destroyed with it, which %s cannot do", windowPos(n), platform)
				}
			}
		}
		return nil
	}
	return walk(pkg.Body, false)
}

// passWindowSurface refuses what a window says about being on screen, on a
// target that renders `window` as the builtin it is marked -- html, where a
// window is a document or a route until Phase C2 moves pages to `nav.page`. A
// page cannot take itself off screen and is told of no close it could answer,
// so a `:visible` binding, a `visible` other than the default and an
// `@closed` each say something that would be emitted and ignored.
//
// A target that overrides `ui.window` has composed its windows away by now
// (composeOverriddenBuiltins), so there is nothing here for it. Phase C3
// deletes this with the builtin.
var passWindowSurface = pass{
	name:    "WindowSurface",
	enabled: func(Features) bool { return true },
	apply:   refuseWindowSurface,
}

func refuseWindowSurface(pkg *ir.Package, _ Features, opts Options) error {
	if pkg == nil || opts.Platform == "" {
		return nil
	}
	for _, w := range ir.AllWindows(pkg) {
		for _, b := range w.Bindings {
			if b.PropName == "visible" {
				return fmt.Errorf("%s: %s cannot show or hide a window, so its `visible` cannot be bound", windowPos(w), opts.Platform)
			}
		}
		if v := w.Prop("visible"); v != nil {
			if lit, ok := v.(*ir.Literal); !ok || lit.Value != "true" {
				return fmt.Errorf("%s: %s cannot show or hide a window, so its `visible` is always true", windowPos(w), opts.Platform)
			}
		}
		for _, h := range w.Handlers {
			if h.Name == "closed" {
				return fmt.Errorf("%s: %s reports no close of a window, so its `@closed` would never run", windowPos(w), opts.Platform)
			}
		}
	}
	return nil
}
