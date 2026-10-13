package checker

import (
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkEffectChanges holds an effect's report handlers to the key they report
// on (ir.EffectEventRefusal): `@update` needs a key, and `@insert`, `@set`,
// `@remove` and `@clear` describe a write to a list, so they need a key that is
// a list, and one held in a cell -- a var, a prop, a
// field of either -- since a change to `xs.filter(f)` is not a change any write
// site describes. And they need `@update` beside them, since replacing the list
// whole is a write none of them describes, and a program handling only the
// changes would miss it without a word.
//
// Each is refused rather than left to fire never: an event that cannot fire on
// the key it was written against is a handler the program thinks is running.
func (c *checker) checkEffectChanges(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	for _, o := range ir.Owners(pkg) {
		_ = ir.Walk(o.Stmts(), func(n ir.Node) error {
			if inst, isNode := n.(*ir.NodeInst); isNode && inst.Component != nil && inst.Component.Builtin == ir.BuiltinEffect {
				c.checkEffectChangeHandlers(inst)
			}
			return nil
		})
	}
}

func (c *checker) checkEffectChangeHandlers(inst *ir.NodeInst) {
	var deltas []ir.EventHandler
	hasUpdate := false
	for _, h := range inst.Handlers {
		switch {
		case h.Name == ir.EffectUpdate:
			hasUpdate = true
		case slices.Contains(ir.EffectDeltas, h.Name):
			deltas = append(deltas, h)
		default:
			// @mount and @unmount on a key that does not compare are the
			// rebuild-key rule's, which follows a generic key to the call
			// site that binds it.
			continue
		}
		if why := ir.EffectEventRefusal(h.Name, inst.EffectKey()); why != "" {
			c.error(handlerPos(inst, h), "an effect's @%s %s", h.Name, why)
			return
		}
	}
	if len(deltas) > 0 && !hasUpdate {
		c.error(handlerPos(inst, deltas[0]), "an effect handling @%s must handle @update too: replacing the list whole is a write no change event describes", deltas[0].Name)
	}
}

// effectBrackets reports whether an effect handles either side of its
// lifetime, which is what a key that does not compare cannot identify.
func effectBrackets(inst *ir.NodeInst) bool {
	for _, h := range inst.Handlers {
		if h.Name == ir.EffectMount || h.Name == ir.EffectUnmount {
			return true
		}
	}
	return false
}

func handlerPos(inst *ir.NodeInst, h ir.EventHandler) ast.Pos {
	if h.AST != nil {
		return h.AST.Pos
	}
	return ir.StmtPos(inst)
}
