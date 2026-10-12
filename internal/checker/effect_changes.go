package checker

import (
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkEffectChanges holds an effect's change handlers to the key they report
// on: `@insert`, `@set`, `@remove` and `@clear` describe a write to a list, so
// they need a key that is a list, and one held in a cell -- a var, a prop, a
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
		}
	}
	if len(deltas) == 0 {
		return
	}
	key := effectKey(inst)
	for _, h := range deltas {
		pos := handlerPos(inst, h)
		switch {
		case key == nil:
			c.error(pos, "an effect's @%s reports a change to the list `on` holds, and this effect has no `on`", h.Name)
			return
		case key.ExprType() == nil || key.ExprType().Kind != ir.TypeList:
			c.error(pos, "an effect's @%s reports a change to a list, and `on` is %s; handle @update to react to a write", h.Name, key.ExprType())
			return
		case !isCellPath(key):
			c.error(pos, "an effect's @%s reports a change to the list a var or prop holds, and `on` is computed from one; key the effect on the list itself, or handle @update", h.Name)
			return
		}
	}
	if !hasUpdate {
		c.error(handlerPos(inst, deltas[0]), "an effect handling @%s must handle @update too: replacing the list whole is a write no change event describes", deltas[0].Name)
	}
}

// effectKey is the value an effect's `on` was given, or nil.
func effectKey(inst *ir.NodeInst) ir.Expr {
	for _, p := range inst.Props {
		if p.Name == ir.EffectOn {
			return p.Value
		}
	}
	return nil
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

// isCellPath is whether e names storage a write lands in -- a var, a prop or a
// parameter, or a field of one -- rather than a value computed from it.
func isCellPath(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		switch x.Sym.(type) {
		case *ir.Var, *ir.Param:
			return true
		}
	case *ir.Select:
		if t := x.Operand.ExprType(); t == nil || t.Kind != ir.TypeStruct {
			return false
		}
		return isCellPath(x.Operand)
	}
	return false
}
