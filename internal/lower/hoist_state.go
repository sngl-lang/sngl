package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passHoistState makes a window's top-level `var` a declaration of the thing
// that contains the window, rather than a local of its body.
//
// The checker collects a component's `var` into comp.Vars, which is what makes
// it state; a window's it leaves as an *ir.LocalVar statement in the body. No
// platform implements that shape. It only looked implemented, because the
// initializer still appeared in the generated output -- as a local of the
// render function, reset on every frame, with the Model field it is assigned
// from nowhere declared. bubbletea emitted Go that does not compile, android
// emitted Kotlin referring to an undeclared name, and html emitted nothing at
// all.
//
// The container and not the window, because a window is a rendering root and
// not a storage level: the three single-process targets put every window's
// state in one Model already, and html writing a copy into each page is that
// platform instantiating one declaration rather than a level of its own.
// `window` is root-only, so the container is the package unless a root-family
// component renders it.
//
// Normalising here rather than in the checker keeps a window body a block as
// written.
var passHoistState = pass{
	name:    "HoistState",
	enabled: func(Caps) bool { return true },
	apply:   applyHoistState,
}

func applyHoistState(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	// The package is a state owner like the other two: a `var` at the top of
	// its body is the package's state, not a local of it. Nothing fills
	// Package.Body yet, so this is inert -- it is here because the rule is the
	// same rule, and a pass that knows two of the three owners is how the
	// window case went missing in the first place.
	pkg.Body, pkg.Vars = promoteLocalVarsToVars(pkg.Body, pkg.Vars)
	hoistWindowsIn(pkg.Body, &pkg.Vars)
	for _, comp := range pkg.Components {
		if comp != nil {
			hoistWindowsIn(comp.Body, &comp.Vars)
		}
	}
	// A window at the root of a file is in no body, so the walk above does not
	// reach it; its container is the package either way.
	for _, w := range pkg.Windows {
		if w != nil {
			w.Body, pkg.Vars = promoteLocalVarsToVars(w.Body, pkg.Vars)
		}
	}
	return nil
}

// hoistWindowsIn promotes the top-level vars of every window a block renders
// into that block's owner, reaching through what says when and how many.
func hoistWindowsIn(stmts []ir.Stmt, into *[]*ir.Var) {
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		if w, ok := s.(*ir.Window); ok {
			w.Body, *into = promoteLocalVarsToVars(w.Body, *into)
		}
		return nil
	})
}
