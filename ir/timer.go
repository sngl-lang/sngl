package ir

import "strings"

// A recurring deadline is a node in the tree and nothing else.
//
// `timer` is an ordinary component in `sngl:time` with no body; what a timer
// means is each target's override of it. html, fyne and gtk4 override it with
// an `effect` over a start/stop pair of host natives, so those trees hold no
// timer node at all. bubbletea, android and none override it with an
// #[intrinsic] primitive that stays where it was written -- which is what
// answers the branch, the component boundary and the loop it was written
// inside, without anything having to ask.
//
// There used to be an ir.Timer record and a lowering pass that lifted the
// primitive out of the tree onto its owner. Every field of it -- the interval,
// the gate, the tick body -- is readable off the node, so the record carried
// nothing the tree did not and the lift cost a Timers field on three owners
// plus a timer arm in nineteen walks. The interpreter never had either and
// read the tree directly.

// TimerRole is the second half of a platform's timer primitive id. The id is
// namespaced by the platform that emits it -- `bubbletea:Timer`,
// `android:Timer` -- so only the target being built for can have contributed
// one to this tree, and the role is what they have in common.
const TimerRole = "Timer"

// The primitive's two props and its one event, named here so a change to the
// declaration fails in one place.
const (
	TimerIntervalProp = "interval"
	TimerEnabledProp  = "enabled"
	TimerTickEvent    = "tick"
)

// IsTimerPrimitive reports whether a component is some platform's timer
// primitive. Read off the declaration's own mark: a registry would be a second
// place to say it and a second place to forget.
func IsTimerPrimitive(c *Component) bool {
	if c == nil || c.Intrinsic == "" {
		return false
	}
	ns, name, ok := strings.Cut(c.Intrinsic, ":")
	return ok && ns != "" && name == TimerRole
}
