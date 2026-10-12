package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// refuseEffectChanges reports an effect handling `@update` or a list's change
// events, which no lowering implements yet: passEffect and each target that
// brackets a lifetime natively know `@mount` and `@unmount` and nothing else,
// so the handler would be emitted nowhere and never run. The interpreter
// answers them (`sngl test` on none, which runs the checked program unlowered),
// which is what the language is held to until a target does.
func refuseEffectChanges(pkg *ir.Package) error {
	var bad error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || bad != nil || inst.Component == nil || inst.Component.Builtin != ir.BuiltinEffect {
			return nil
		}
		for _, h := range inst.Handlers {
			if h.Name == ir.EffectUpdate || slices.Contains(ir.EffectDeltas, h.Name) {
				pos := ir.NodePos(inst)
				if h.AST != nil {
					pos = h.AST.Pos
				}
				bad = fmt.Errorf("%s: an effect's @%s is not implemented on any compiled target yet; `sngl test` runs it on the interpreter", pos, h.Name)
				return nil
			}
		}
		return nil
	})
	return bad
}
