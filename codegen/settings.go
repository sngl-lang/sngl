package codegen

import (
	"strings"

	"duckfam.us/sngl/ir"
)

// UsesSettings reports whether pkg reaches sngl:settings' store, which the
// program's own entry point then opens on each target.
func UsesSettings(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil && strings.HasPrefix(c.Func.Intrinsic, "settings.") {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
