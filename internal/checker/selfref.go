package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// reportSelfReferentialProps reports an argument of a node that reads the very
// `#id` the node is declaring. The id names the instance this argument list is
// building, so the read has nothing to resolve to yet whatever a target does
// with it -- `window #h(title = h.title)` asks the title for the title.
//
// Nothing refused it, and each consumer that met one grew a guard of its own
// instead: `optimize.evalCtx.foldingProp` leaves such a prop standing so the
// fold terminates, which is a page emitted with no <title> and nothing said.
// CLAUDE.md names that guard, and the sibling `const a int = a`, as the
// survivable answer rather than the right one; this is the right one for the
// half a node's id covers. The const case is a scope of its own and is
// untouched.
//
// Matched by symbol, not by name. `declareNodeID` declines to bind an id that
// an outer scope already holds, so a `#foo` written beside an existing `foo`
// reads that one, which is an ordinary read of something that does exist.
func (c *checker) reportSelfReferentialProps(pos ast.Pos, id string, handle *ir.Var, props []ir.Arg) {
	if id == "" || handle == nil {
		return
	}
	for _, p := range props {
		reported := false
		_ = ir.Walk(p.Value, func(n ir.Node) error {
			ident, ok := n.(*ir.Ident)
			if !ok || reported || ident.Sym != ir.Symbol(handle) {
				return nil
			}
			reported = true
			at := pos
			if ident.AST != nil {
				at = ident.AST.Pos
			}
			c.error(at, "#%s names the node this argument list declares, so %s cannot read it", id, argDesc(p))
			return nil
		})
	}
}

// argDesc names an argument for a diagnostic: its own name where it has one,
// and its role where it is positional.
func argDesc(a ir.Arg) string {
	if a.Name == "" {
		return "the same node's argument"
	}
	return "its own " + a.Name + " argument"
}
