package codegen

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// timerRole is the name a platform gives the primitive its `time.timer`
// override schedules with. The id is namespaced by the platform that emits it
// -- `bubbletea:Timer`, `fyne:Timer` -- so the role is the half after the
// colon, and only the target being generated for can have contributed one to
// the tree.
//
// Read off the declaration rather than from a registry: a platform that
// declares the primitive has said everything there is to say, and a second
// place to register it is a second place to forget.
const timerRole = "Timer"

// IsTimerPrimitive reports whether a component is some platform's timer
// primitive.
func IsTimerPrimitive(c *ir.Component) bool {
	if c == nil || c.Intrinsic == "" {
		return false
	}
	ns, name, ok := strings.Cut(c.Intrinsic, ":")
	return ok && ns != "" && name == timerRole
}

// collectTimerPrimitives walks the emitted tree and returns one TimerInfo per
// placed timer primitive, in tree order.
//
// A walk rather than a list off the declaration. `ir.Timer` was hoisted onto
// `Component.Timers` at check time and read back as `pkg.Timers` plus `main`'s,
// so a timer in any other component was never emitted at all -- it was checked,
// type-correct and silently inert. The primitive is an ordinary node, so it is
// found wherever the program put it and wherever inlining moved it.
func collectTimerPrimitives(pkg *ir.Package) []TimerInfo {
	if pkg == nil {
		return nil
	}
	var out []TimerInfo
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || !IsTimerPrimitive(inst.Component) {
			return nil
		}
		var tick *ir.Func
		for i := range inst.Handlers {
			if inst.Handlers[i].Name == "tick" {
				tick = inst.Handlers[i].Func
			}
		}
		if tick == nil {
			return nil
		}
		info := TimerInfo{
			Index:      len(out),
			IntervalMs: IntervalToMs(NodeProp(inst, "interval")),
			Body:       tick.Block,
			LocalRefs:  tick.LocalRefs,
		}
		// A gate that is not a bare state read is left empty, which every
		// backend already reads as "always on" -- the same answer they gave
		// when ir.Timer.Enabled was any expression but an identifier.
		if id, ok := NodeProp(inst, "enabled").(*ir.Ident); ok {
			info.ActiveVar = id.Name
		}
		out = append(out, info)
		return nil
	})
	return out
}
