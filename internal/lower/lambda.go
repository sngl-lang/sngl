package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passLambda = pass{
	name:    "NoLambda",
	enabled: func(c Caps) bool { return c.NoLambda },
	apply:   lowerLambda,
}

// lowerLambda lifts closures to top-level functions plus captured-state
// structs. Requires accurate capture analysis from the checker.
// Phase 1: stub — implementation lands in Phase 2 or later.
func lowerLambda(pkg *ir.Package) error { return nil }
