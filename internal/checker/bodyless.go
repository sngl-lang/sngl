package checker

import "git.duckfam.us/jonathan/sngl/ir"

// reportBodylessComponents requires a component declared with no body to get
// one from somewhere the checker can name.
//
// `{}` and no braces at all are different declarations: the first says the
// component renders nothing, which is a legitimate thing for one to say, and
// the second says the render comes from elsewhere. This is where the second is
// required to be true rather than assumed -- the counterpart of the rule
// checkFuncBody applies to a signature. Without it a bodyless declaration
// renders nothing, silently, on every target that has no override for it.
//
// Asked after pass2 because that is when the answer is complete: a platform
// package's overrides are collected by checkPendingExtensions and a program's
// own by collectUserOverrides, both of which run before pass2, but an
// override's own body is checked during it.
func (c *checker) reportBodylessComponents() {
	if c.pkg == nil {
		return
	}
	for _, comp := range c.pkg.Components {
		if comp.AST == nil || comp.AST.Body.IsDefined() || renderSuppliedElsewhere(comp) {
			continue
		}
		c.error(comp.AST.Pos, "component %q has no body: give it one, or say where the render comes from — #[intrinsic] or a per-target override", comp.Name)
	}
}

// renderSuppliedElsewhere reports whether a bodyless component's render comes
// from somewhere a declaration names.
//
// Three answers, and the third has no counterpart for a function: a
// #[builtin] node kind is dispatched by the compiler itself, so the render is
// an IR construct rather than any body.
func renderSuppliedElsewhere(comp *ir.Component) bool {
	return comp.Intrinsic != "" || comp.Builtin != ir.BuiltinNone ||
		len(comp.PlatformOverrides) > 0 || len(comp.LanguageOverrides) > 0
}
