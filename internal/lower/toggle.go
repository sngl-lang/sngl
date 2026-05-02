package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passToggle = pass{
	name:    "NoToggle",
	enabled: func(c Caps) bool { return c.NoToggle },
	apply:   lowerToggle,
}

// lowerToggle rewrites toggle statements (x!!) into assignments (x = !x).
// Phase 1: stub — implementation lands in Phase 2.
func lowerToggle(pkg *ir.Package) error { return nil }
