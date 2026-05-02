package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passReactivity = pass{
	name:    "NoReactivity",
	enabled: func(c Caps) bool { return c.NoReactivity },
	apply:   lowerReactivity,
}

// lowerReactivity analyzes which mutations affect which visual nodes and
// injects explicit updater statements after each mutation. Owns dataflow
// analysis (currently in codegen/analysis.go and codegen/deps.go; copied
// here in Phase 3).
// Phase 1: stub — implementation lands in Phase 3.
func lowerReactivity(pkg *ir.Package) error { return nil }
