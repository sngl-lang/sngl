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
	apply   func(*ir.Package, Caps, Options) error
}

// passes is the fixed execution order. Earlier passes may not depend on
// transformations performed by later ones; later passes may. Order rationale:
//  0. RefLoop — rewrites &-bound loop element refs to indexed list access
//     (list[idx]). Runs before NoReactivity so the resulting list[idx].field
//     writes are seen as mutations of the list var, and before NoToggle so a
//     toggled element-ref target is rewritten first.
//  1. NoUnit, NoEnum — collapse types, no deps.
//  2. NoTernary — rewrites expressions, no deps on visual model.
//  3. NoAsyncReactive — must run before NoComputed (introduces sync state vars
//     that NoComputed would otherwise inline away) and before NoReactivity
//     (synthetic vars must be visible as reactive deps).
//  4. NoComputed — must run before NoReactivity (plain reads vs. computed indirections).
//  5. NoLambda — must run before NoReactivity (helpers may inject closures otherwise).
//  6. NoToggle — cheap stmt rewrite; before NoReactivity so the assignment is visible.
//  7. NoReactivity — analyzes dataflow, injects updaters.
//     7a. InlinePure — always on; inlines pure user components and (under
//     NoStdlibWrappers) platform-stdlib wrappers. Runs after reactivity
//     wires user-level deps and before declarative flattening.
//     7b. NoInlineComponents — opt-in. Inlines every non-recursive user
//     component into main, renaming vars/funcs/timers and substituting
//     prop refs with call-site arg exprs. After this pass, codegen on
//     opted-in targets sees only main + any recursive components.
//     7c. NoImplicitRecv — opt-in. Normalizes method calls to always carry
//     the receiver in Args[0], enabling codegen to drop conditional logic.
//  8. NoTimer — depends on reactivity decisions (timer handlers may have been wrapped).
//  9. NoDeclarative — flattens the visual tree, destroying shape earlier passes used;
//     its lifter (when NoLambda is also active) may emit fresh ref<T> shapes for
//     handlers promoted from inline blocks.
//  10. NoRef — runs last so it catches every ref<T> shape any earlier pass may have
//     emitted, including those produced by NoDeclarative's lifter. Idempotent: when
//     no ref<T> survives, all rewrites are no-ops.
var passes = []pass{
	passPlatformExtensionBody,
	passRefLoop,
	passUnit,
	passEnum,
	passTernary,
	passAsyncReactive,
	passComputed,
	passLambda,
	passNoListLambdas,
	passToggle,
	passContext,
	passInlinePure,
	passNoInlineComponents,
	passNoImplicitRecv,
	passReactivity,
	passTimer,
	passDeclarative,
	passNoRef,
}

// Options controls a single Lower invocation.
type Options struct {
	// StopAfter, when non-empty, stops the pipeline after the named pass
	// runs (matching pass.name, e.g. "NoToggle"). Special value "none"
	// runs no passes — useful for the dump command's "show pre-lowering
	// state" mode. Empty string runs all enabled passes.
	StopAfter string

	// Platform is the active build target's platform identifier (e.g.
	// "html", "gtk4", "fyne"). passPlatformExtensionBody uses it to
	// swap each *ir.Component's matching PlatformBodies entry into
	// Component.Body. Empty string disables the swap — appropriate for
	// platform-agnostic tools (LSP, format) that should leave abstract
	// stdlib components abstract.
	Platform string
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
		if err := p.apply(pkg, caps, opts); err != nil {
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
