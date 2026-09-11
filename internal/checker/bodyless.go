package checker

import "git.duckfam.us/jonathan/sngl/ir"

// reportBodylessComponents requires a component declared with no body to get
// one from somewhere the checker can name, the counterpart of the rule
// checkFuncBody applies to a bodyless func.
//
// Scoped to the program's own package: a library package is loaded by
// loadStdlibPackage, which runs pass1 alone, so no lib/ or sngl:platform/
// declaration is ever asked. That is why the diagnostic names only the
// override -- the two marks below cannot be written by a program, and the
// declarations that do carry them are not reached. Making lib/ shapes
// bodyless (#213 step 3) needs this to run from that loader too, and needs a
// fourth answer there for a tree member whose render comes from passCanvas.
func (c *checker) reportBodylessComponents() {
	if c.pkg == nil {
		return
	}
	for _, comp := range c.pkg.Components {
		if comp.AST == nil || !comp.Bodyless || renderSuppliedElsewhere(comp) {
			continue
		}
		c.error(comp.AST.Pos, "component %q has no body: give it one, or say where the render comes from with a per-target override", comp.Name)
	}
}

// renderSuppliedElsewhere reports whether a bodyless component's render comes
// from somewhere a declaration names. Runs after every route that populates
// the override maps -- mergeTargetExtensions in newChecker, collectUserOverrides
// and checkPendingExtensions before pass2.
func renderSuppliedElsewhere(comp *ir.Component) bool {
	return comp.Intrinsic != "" || comp.Builtin != ir.BuiltinNone ||
		len(comp.PlatformOverrides) > 0 || len(comp.LanguageOverrides) > 0
}
