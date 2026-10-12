package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// refuseEffectChanges reports an effect handler no lowering implements on
// this target, which would otherwise be emitted nowhere and never run.
//
// A list's change events are lowered nowhere yet. `@update` is lowered by
// passEffect, so a target that brackets a lifetime natively -- android's
// DisposableEffect, which knows mount and unmount -- does not have it. The
// interpreter answers both (`sngl test` on none runs the checked program
// unlowered), which is what the language is held to until a target does.
func refuseEffectChanges(pkg *ir.Package, feats Features) error {
	var bad error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || bad != nil || inst.Component == nil || inst.Component.Builtin != ir.BuiltinEffect {
			return nil
		}
		for _, h := range inst.Handlers {
			delta := slices.Contains(ir.EffectDeltas, h.Name)
			if !delta && (h.Name != ir.EffectUpdate || !feats.Effects) {
				continue
			}
			pos := ir.NodePos(inst)
			if h.AST != nil {
				pos = h.AST.Pos
			}
			where := "any compiled target"
			if !delta {
				where = "a target that brackets a lifetime natively"
			}
			bad = fmt.Errorf("%s: an effect's @%s is not implemented on %s yet; `sngl test` runs it on the interpreter", pos, h.Name, where)
			return nil
		}
		return nil
	})
	return bad
}
