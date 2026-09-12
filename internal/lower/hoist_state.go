package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passHoistState makes a window's top-level `var` a declaration of that window
// rather than a local of its body.
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
// Normalising here rather than in the checker keeps a window body a block as
// written and gives every consumer one place to read window state from, which
// is the same place it already reads a component's.
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
	for _, w := range allWindows(pkg) {
		w.Body, w.Vars = promoteLocalVarsToVars(w.Body, w.Vars)
	}
	return nil
}

// allWindows is every window the package holds, in ir.Owners' order: those at
// the root of a file and those a component body renders. Derived from Owners
// so the two answers to "which declarations own state" cannot drift apart.
func allWindows(pkg *ir.Package) []*ir.Window {
	var out []*ir.Window
	for _, o := range ir.Owners(pkg) {
		if o.Win != nil {
			out = append(out, o.Win)
		}
	}
	return out
}
