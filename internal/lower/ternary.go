package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passTernary = pass{
	name:    "NoTernary",
	enabled: func(c Caps) bool { return c.NoTernary },
	apply:   lowerTernary,
}

// lowerTernary rewrites a ? b : c expressions into if/else statements with
// a temporary variable; the original expression is replaced by a reference
// to that temp.
// Phase 1: stub — implementation lands in Phase 2.
func lowerTernary(pkg *ir.Package) error { return nil }
