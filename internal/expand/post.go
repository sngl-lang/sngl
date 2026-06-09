package expand

import "git.duckfam.us/jonathan/sngl/ir"

// ExpandPost runs post-check macro expansion on pkg. Currently a no-op.
func ExpandPost(pkg *ir.Package) []ir.Diagnostic {
	return nil
}
