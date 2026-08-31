package interp

import (
	"fmt"
	"time"

	"git.duckfam.us/jonathan/sngl/ir"
)

// Clock is the interpreter's notion of time.
//
// It exists because a test and a window need opposite answers from the same
// code. A test advances time explicitly and must be reproducible; a window
// follows the wall clock. Without one seam, timers get written twice -- once
// for `t.tick()` and again for the event loop -- and the two drift.
type Clock interface {
	Now() time.Time
}

// Advanceable is a Clock whose time is moved by the program rather than by the
// world. Only a virtual clock is.
type Advanceable interface {
	Clock
	Advance(time.Duration)
}

// Epoch is where a Virtual clock starts. Fixed, because a snapshot that
// interpolates a date must not depend on when the suite ran.
var Epoch = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// Virtual is a clock the program advances. Used by tests and by anything that
// has to replay a schedule deterministically.
type Virtual struct{ now time.Time }

func NewVirtual() *Virtual { return &Virtual{now: Epoch} }

func (v *Virtual) Now() time.Time          { return v.now }
func (v *Virtual) Advance(d time.Duration) { v.now = v.now.Add(d) }
func (v *Virtual) Set(t time.Time)         { v.now = t }

// Real is the wall clock, for a running window.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Timers is the firing schedule for one component's timers.
//
// The interpreter previously had no schedule at all: every timer fired once per
// `t.tick()`, so ir.Timer.Interval was parsed, checked, and then ignored. A
// 100ms timer and a 5s timer advanced at the same rate, which is invisible in a
// fixture with one timer and wrong everywhere else.
type Timers struct {
	clock   Clock
	entries []*timerEntry
}

type timerEntry struct {
	// Key identifies the timer across a reload. A timer is written as a node
	// but the checker hoists it out of the body onto Component.Timers, so its
	// path is positional within that list.
	Key      Key
	Timer    *ir.Timer
	Interval time.Duration
	// next is when this timer fires again. Carried across a reload when the
	// interval is unchanged, so reloading does not reset every timer's phase.
	next time.Time
}

// NewTimers builds the schedule for env's component. The interval is an
// expression -- it may name a var or a unit-suffixed constant -- so it is
// evaluated against env rather than read off the IR.
func NewTimers(clock Clock, env *Env) (*Timers, error) {
	ts := &Timers{clock: clock}
	if env == nil || env.Comp == nil {
		return ts, nil
	}
	now := clock.Now()
	for i, t := range env.Comp.Timers {
		d, err := intervalOf(env, t)
		if err != nil {
			return nil, fmt.Errorf("timer %d of %s: %w", i, env.Comp.Name, err)
		}
		ts.entries = append(ts.entries, &timerEntry{
			Key:      TimerKey(env.Comp.Name, i),
			Timer:    t,
			Interval: d,
			next:     now.Add(d),
		})
	}
	return ts, nil
}

// intervalOf evaluates a timer's interval to a duration. `unit duration` bases
// on ms (lib/time/time.sngl), so the evaluated number is milliseconds.
func intervalOf(env *Env, t *ir.Timer) (time.Duration, error) {
	if t.Interval == nil {
		return 0, fmt.Errorf("no interval")
	}
	v, err := env.Eval(t.Interval)
	if err != nil {
		return 0, err
	}
	ms := toFloat(v)
	if ms <= 0 {
		return 0, fmt.Errorf("interval is %v, want a positive duration", v)
	}
	return time.Duration(ms * float64(time.Millisecond)), nil
}

// Next reports when the earliest timer fires, and false when there are none.
func (ts *Timers) Next() (time.Time, bool) {
	var out time.Time
	found := false
	for _, e := range ts.entries {
		if !found || e.next.Before(out) {
			out, found = e.next, true
		}
	}
	return out, found
}

// FireDue runs every timer due at the clock's current time and reschedules it.
// This is what a window's event loop calls.
//
// A disabled timer is rescheduled without firing rather than left behind, so
// re-enabling it does not deliver a burst of the fires it missed. That is what
// `enabled` means in lib/time: a gate, not a pause.
func (ts *Timers) FireDue(env *Env) (int, error) {
	now := ts.clock.Now()
	fired := 0
	for _, e := range ts.entries {
		if e.next.After(now) {
			continue
		}
		e.next = now.Add(e.Interval)

		enabled := true
		if e.Timer.Enabled != nil {
			v, err := env.Eval(e.Timer.Enabled)
			if err != nil {
				return fired, err
			}
			if b, ok := v.(bool); ok {
				enabled = b
			}
		}
		if !enabled || e.Timer.Handler == nil {
			continue
		}
		// ExecBlock rather than a raw Exec loop: a `return` in a handler ends
		// that handler, and only ExecBlock swallows the signal.
		if err := env.ExecBlock(e.Timer.Handler.Block); err != nil {
			return fired, err
		}
		fired++
	}
	return fired, nil
}

// Tick advances a virtual clock to the moment the next timer is due and fires
// it. This is `t.tick()`: one tick is one timer deadline, not one fire of
// everything, so a fast timer and a slow one advance at their declared rates.
func (ts *Timers) Tick(env *Env) (int, error) {
	adv, ok := ts.clock.(Advanceable)
	if !ok {
		return 0, fmt.Errorf("cannot tick a %T; only a virtual clock advances", ts.clock)
	}
	next, any := ts.Next()
	if !any {
		return 0, nil
	}
	if d := next.Sub(adv.Now()); d > 0 {
		adv.Advance(d)
	}
	return ts.FireDue(env)
}

// Rebase carries firing phase from a previous schedule across a reload.
//
// A timer whose key and interval both survive keeps its deadline: reloading a
// file must not reset every timer in the program. A changed interval is a
// different timer as far as phase goes, and is scheduled fresh -- carrying a
// deadline computed from the old interval would fire it at neither rate.
func (ts *Timers) Rebase(prev *Timers) {
	if prev == nil {
		return
	}
	old := make(map[Key]*timerEntry, len(prev.entries))
	for _, e := range prev.entries {
		old[e.Key] = e
	}
	for _, e := range ts.entries {
		p, ok := old[e.Key]
		if ok && p.Interval == e.Interval {
			e.next = p.next
		}
	}
}

// NextFor reports when the timer with the given key fires. The reconciler needs
// per-timer access to decide what a reload carried, and so does a test that
// checks it.
func (ts *Timers) NextFor(key Key) (time.Time, bool) {
	for _, e := range ts.entries {
		if e.Key == key {
			return e.next, true
		}
	}
	return time.Time{}, false
}

// TimerKey is the key of a component's nth timer.
func TimerKey(comp string, i int) Key {
	return Key{Comp: comp, Path: fmt.Sprintf("timer@%d", i)}
}
