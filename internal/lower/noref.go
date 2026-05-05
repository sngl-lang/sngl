package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passNoRef = pass{
	name:    "NoRef",
	enabled: func(c Caps) bool { return c.NoRef },
	apply:   lowerNoRef,
}

// lowerNoRef boxes every addressed binding into a synthesized one-field
// reference-semantic struct. After this pass no *ir.TypeRef remains.
//
// Phase C — body lands in subsequent tasks.
func lowerNoRef(pkg *ir.Package, _ Caps) error {
	return nil
}
