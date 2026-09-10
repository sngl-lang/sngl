package checker

import (
	"maps"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

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

// reportBodylessLibComponents asks the same question of a library declaration,
// and asks it per target: the sweep above accepts one override for any target,
// because a program is checked without knowing which target a build picks,
// whereas here the build's targets are resolved and each one either has an
// implementation or does not.
//
// That difference is the point. A `lib/` shape with no override for the target
// being built renders nothing on it, silently, and asking per target is what
// turns that into a build failure -- so an implementation gap has to be
// answered rather than skipped in a switch.
//
// Runs at the end of CheckPackage because that is when every lib package is
// loaded and every target's overrides are merged: a package loads on import,
// or when mergeTargetExtensions resolves an override's base, and both can
// happen after newChecker.
func (c *checker) reportBodylessLibComponents() {
	// No resolved target is LSP, `sngl fmt`, or multi-target discovery: there
	// is nothing to be missing an implementation for.
	if len(c.targets) == 0 || c.libs == nil {
		return
	}
	for _, path := range slices.Sorted(maps.Keys(c.libs.pkgs)) {
		pkg := c.libs.pkgs[path]
		if pkg == nil {
			continue
		}
		for _, comp := range pkg.Components {
			if comp == nil || comp.AST == nil || !comp.Bodyless {
				continue
			}
			if comp.Intrinsic != "" || comp.Builtin != ir.BuiltinNone {
				continue
			}
			for _, t := range c.targets {
				if hasOverrideFor(comp, t) {
					continue
				}
				c.error(comp.AST.Pos, "component %q in sngl:%s has no body and no implementation for %s: every target it can be built for needs one",
					comp.Name, path, describeTarget(t))
			}
		}
	}
}

// hasOverrideFor mirrors ir.SpecializeForTarget's choice: the platform's
// override answers first, and the language's is the fallback.
func hasOverrideFor(comp *ir.Component, t ir.StaticTarget) bool {
	if t.Platform != "" && comp.PlatformOverrides != nil {
		if _, ok := comp.PlatformOverrides[t.Platform]; ok {
			return true
		}
	}
	if t.Language != "" && comp.LanguageOverrides != nil {
		if _, ok := comp.LanguageOverrides[t.Language]; ok {
			return true
		}
	}
	return false
}

func describeTarget(t ir.StaticTarget) string {
	switch {
	case t.Platform != "" && t.Language != "":
		return "platform " + t.Platform + " with language " + t.Language
	case t.Platform != "":
		return "platform " + t.Platform
	default:
		return "language " + t.Language
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
