package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passUnit = pass{
	name:    "NoUnit",
	enabled: func(c Caps) bool { return c.NoUnit },
	apply:   lowerUnit,
}

// lowerUnit collapses unit-typed values to their underlying int.
// Phase 1: stub — implementation lands in Phase 2.
func lowerUnit(pkg *ir.Package) error { return nil }
