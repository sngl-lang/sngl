package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// pass is one lowering pass: a name (matching its Caps field), a predicate
// over Caps for whether it runs, and the apply function that mutates pkg in
// place.
type pass struct {
	name    string
	enabled func(Caps) bool
	apply   func(*ir.Package) error
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
