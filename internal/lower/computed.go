package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passComputed = pass{
	name:    "NoComputed",
	enabled: func(c Caps) bool { return c.NoComputed },
	apply:   lowerComputed,
}

// lowerComputed resolves computed variables either by inlining the
// expression at each use site or by hoisting to a memoized func. Must run
// before NoReactivity so dataflow sees plain reads, not computed
// indirections.
// Phase 1: stub — implementation lands in Phase 3.
func lowerComputed(pkg *ir.Package) error { return nil }
