package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// pass is one lowering pass: a name (matching its Caps field), a predicate
// over Caps for whether it runs, and the apply function that mutates pkg in
// place. apply receives the full Caps so cross-cap-aware passes can branch
// (e.g. NoDeclarative checks NoLambda before lifting promoted handlers).
type pass struct {
	name    string
	enabled func(Caps) bool
	apply   func(*ir.Package, Caps) error
}

// passes is the fixed execution order. Earlier passes may not depend on
// transformations performed by later ones; later passes may. Order rationale:
//  1. NoUnit, NoEnum — collapse types, no deps.
//  2. NoTernary — rewrites expressions, no deps on visual model.
//  3. NoComputed — must run before NoReactivity (plain reads vs. computed indirections).
//  4. NoLambda — must run before NoReactivity (helpers may inject closures otherwise).
//  5. NoToggle — cheap stmt rewrite; before NoReactivity so the assignment is visible.
//  6. NoReactivity — analyzes dataflow, injects updaters.
//  7. NoTimer — depends on reactivity decisions (timer handlers may have been wrapped).
//  8. NoDeclarative — last; flattens the visual tree, destroying shape earlier passes used.
var passes = []pass{
	passUnit,
	passEnum,
	passTernary,
	passComputed,
	passLambda,
	passToggle,
	passReactivity,
	passTimer,
	passDeclarative,
}

// Options controls a single Lower invocation.
type Options struct {
	// StopAfter, when non-empty, stops the pipeline after the named pass
	// runs (matching pass.name, e.g. "NoToggle"). Special value "none"
	// runs no passes — useful for the dump command's "show pre-lowering
	// state" mode. Empty string runs all enabled passes.
	StopAfter string
}

// Lower applies all enabled lowering passes to pkg in execution order,
// mutating pkg in place. caps determines which passes run; opts.StopAfter
// optionally short-circuits the pipeline after a named pass.
//
// Returns an error wrapping the failing pass's name when any pass fails or
// when opts.StopAfter names a pass that does not exist.
func Lower(pkg *ir.Package, caps Caps, opts Options) error {
	if opts.StopAfter == "none" {
		return nil
	}
	if opts.StopAfter != "" {
		known := false
		for _, p := range passes {
			if p.name == opts.StopAfter {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("lower: unknown StopAfter %q (valid: %v)", opts.StopAfter, PassNames())
		}
	}
	for _, p := range passes {
		if !p.enabled(caps) {
			continue
		}
		if err := p.apply(pkg, caps); err != nil {
			return fmt.Errorf("lower: pass %s: %w", p.name, err)
		}
		if opts.StopAfter != "" && p.name == opts.StopAfter {
			break
		}
	}
	return nil
}

// PassNames returns the ordered list of pass names. Used for help text and
// error messages.
func PassNames() []string {
	names := make([]string, len(passes))
	for i, p := range passes {
		names[i] = p.name
	}
	return names
}

// EnabledPasses returns the ordered list of pass names that would run for
// the given caps. Used by `dump lowered --list`.
func EnabledPasses(caps Caps) []string {
	var out []string
	for _, p := range passes {
		if p.enabled(caps) {
			out = append(out, p.name)
		}
	}
	return out
}
