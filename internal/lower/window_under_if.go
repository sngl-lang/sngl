package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passWindowUnderIf refuses a window written under an `if` that reads state on
// a target that still renders `window` as the builtin it is marked: html,
// bubbletea and android. Such a window says it exists only while the condition
// holds, and those targets build every window they find once, so emitting it
// would be emitting it as if the `if` were not there.
//
// Where a platform overrides `ui.window` -- gtk4 and fyne, with a Toplevel --
// the window is an ordinary node by now (composeOverriddenBuiltins) and the
// `if` is an ordinary render slot, so there is nothing here to refuse. A
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
