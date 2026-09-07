package interp

import (
	"time"

	"git.duckfam.us/jonathan/sngl/ir"
)

// TickIntrinsic is the id of the primitive `sngl:platform/none` declares for a
// recurring deadline, and the only thing the interpreter knows about timers.
//
// `timer` itself is an ordinary component in `sngl:time` with no body; what a
// timer means is each platform's override of it, and this platform's says
// `if enabled { Tick(interval=interval, @tick { tick() }) }`. So the branch,
// the component boundary and the loop a timer was written inside are answered
// by the tree the override renders into, and nothing here has to ask.
const TickIntrinsic = "none:Timer"

// InterpreterPlatform is the platform identifier the interpreter answers to,
// and the key sngl:platform/none's overrides are recorded at.
const InterpreterPlatform = "none"

// tickIntervalProp and tickEvent are the primitive's two members, named here
// so a change to the declaration fails in one place.
const (
	tickIntervalProp = "interval"
	tickEvent        = "tick"
)

// MountedTimer is one recurring deadline the tree holds. Not among the View's
// nodes, for the reason a MountedEffect is not: it draws nothing, and all it
// wants from the tree is the lifetime that reaching it at all confers.
type MountedTimer struct {
	// Key is the mounted path, which is what says two schedules are the same
	// schedule across a re-mount.
	Key      Key
	Interval time.Duration
	// Tick is the handler body, and Env the scope it runs in -- the scope the
	// primitive was rendered in, so what the handler writes is the state the
	// component that placed the timer holds.
	Tick *ir.Func
	Env  *Env
}

// timerOf reads a recurring deadline off a node instantiation, or reports false
// when the node is not one. Recognised by the intrinsic id its declaration
// carries, never by its name -- `Tick` is shadowable like anything else.
//
// An interval that will not evaluate, or that is not positive, yields no timer
// rather than an error: a schedule nobody can compute is a schedule with no
// deadlines, and taking the mount down over it would make a bad interval fatal
// where a bad prop anywhere else is not.
func timerOf(inst *ir.NodeInst, env *Env, key Key) (MountedTimer, bool) {
	if inst == nil || inst.Component == nil || inst.Component.Intrinsic != TickIntrinsic {
		return MountedTimer{}, false
	}
	t := MountedTimer{Key: key, Env: env}
	for _, arg := range inst.Props {
		if arg.Name != tickIntervalProp {
			continue
		}
		v, err := env.Eval(arg.Value)
		if err != nil {
			return MountedTimer{}, false
		}
		d, err := durationFromMs(v)
		if err != nil {
			return MountedTimer{}, false
		}
		t.Interval = d
	}
	if t.Interval <= 0 {
		return MountedTimer{}, false
	}
	for _, h := range inst.Handlers {
		if h.Name == tickEvent {
			t.Tick = h.Func
		}
	}
	return t, true
}
