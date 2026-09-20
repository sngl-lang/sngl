package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// SpanStyleUnsetColor reports whether a `markup.SpanStyle` color is the one
// that means the run set none.
//
// A span style's five typography fields each need a third state -- the cascade
// has to tell "not set" from "set to the default", or a run inside a bold one
// would un-bold it -- and four of the five say it in their own type: an empty
// family, a zero size, and the `inherit` member each enum leads with. Color has
// no such member, so the sentinel is alpha zero, which is the one value that
// cannot also be a choice, nothing painted with it being visible.
//
// Here rather than in either platform because it is the declaration's rule
// rather than a host's: html emits no `color` for it and bubbletea leaves the
// enclosing run's color alone, and the two are the same answer to the same
// question. The mapping that follows is each host's own.
func SpanStyleUnsetColor(e ir.Expr) bool {
	sl, ok := e.(*ir.StructLit)
	if !ok {
		return false
	}
	for _, f := range sl.Fields {
		if f.Name != "a" {
			continue
		}
		lit, ok := f.Value.(*ir.Literal)
		return ok && lit.Value == "0"
	}
	// No alpha written at all is an opaque color: `#336699` fills it in.
	return false
}
