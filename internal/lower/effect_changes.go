package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// refuseEffectChanges reports an effect handler no lowering implements on
// this target, which would otherwise be emitted nowhere and never run.
//
// `@update` and a list's change events are lowered by passEffect, so a target
// that brackets a lifetime natively -- android's DisposableEffect, which knows
// mount and unmount -- has neither. The interpreter answers both (`sngl test`
// on none runs the checked program unlowered), which is what the language is
// held to until such a target does.
func refuseEffectChanges(pkg *ir.Package, feats Features) error {
	if !feats.Effects {
		return nil
	}
	var bad error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || bad != nil || inst.Component == nil || inst.Component.Builtin != ir.BuiltinEffect {
			return nil
		}
		for _, h := range inst.Handlers {
			if h.Name != ir.EffectUpdate && !slices.Contains(ir.EffectDeltas, h.Name) {
				continue
			}
			pos := ir.NodePos(inst)
			if h.AST != nil {
				pos = h.AST.Pos
			}
			bad = fmt.Errorf("%s: an effect's @%s is not implemented on a target that brackets a lifetime natively yet; `sngl test` runs it on the interpreter", pos, h.Name)
			return nil
		}
		return nil
	})
	return bad
}
