package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// Intrinsic ids of the window methods a platform answers with the host window
// the Model keeps.
const (
	WindowOpenIntrinsic  = "window.open"
	WindowCloseIntrinsic = "window.close"
)

// OpensWindows reports whether pkg calls `open` or `close` on a window. A host
// keeps its window in a Model field only then: an `#id` on a window is also
// how a route or a title is read, and a field nothing opens would be one more
// declaration in every such program.
func OpensWindows(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil &&
			(c.Func.Intrinsic == WindowOpenIntrinsic || c.Func.Intrinsic == WindowCloseIntrinsic) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
