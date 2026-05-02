package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passDeclarative = pass{
	name:    "NoDeclarative",
	enabled: func(c Caps) bool { return c.NoDeclarative },
	apply:   lowerDeclarative,
}

// lowerDeclarative flattens the visual node tree into a stream of explicit
// create / append / update IR calls. Last pass because it destroys the tree
// shape earlier passes rely on.
// Phase 1: stub — implementation lands in Phase 3.
func lowerDeclarative(pkg *ir.Package) error { return nil }
