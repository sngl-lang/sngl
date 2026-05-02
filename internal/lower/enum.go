package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passEnum = pass{
	name:    "NoEnum",
	enabled: func(c Caps) bool { return c.NoEnum },
	apply:   lowerEnum,
}

// lowerEnum collapses enum member references to int constants.
// Phase 1: stub — implementation lands in Phase 2.
func lowerEnum(pkg *ir.Package) error { return nil }
