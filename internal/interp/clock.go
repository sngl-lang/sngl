package interp

import (
	"fmt"
	"time"

	"duckfam.us/sngl/ir"
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

// Timers is the firing schedule the mounted tree describes.
//
// Rebuilt from the View rather than read off a declaration, which is what makes
// a timer's position mean something: one written under a branch that did not
// render describes no deadline, and one in a child component describes one
// because the child rendered it. Read off a flat `Component.Timers` the checker
// hoisted every timer onto, neither held -- and the two failures pointed in
// opposite directions.
//
// The interpreter previously had no schedule at all either: every timer fired
// once per `t.tick()`, so the interval was parsed, checked and then ignored.
type Timers struct {
	clock   Clock
	entries []*timerEntry
}

type timerEntry struct {
	// Key is the mounted path, which is what says two schedules are the same
	// schedule across a re-mount -- the same thing it says for an effect.
	Key      Key
	Interval time.Duration
	Tick     *ir.Func
	Env      *Env
	// next is when this timer fires again. Carried across a re-mount when the
	// interval is unchanged, so re-rendering does not reset every timer's
	// phase, and neither does a reload.
	next time.Time
}

// NewTimers returns an empty schedule. What it holds comes from Retarget: a
// program describes its deadlines by rendering, so there is nothing to read
// here.
func NewTimers(clock Clock) *Timers { return &Timers{clock: clock} }

// Retarget moves the schedule to the one v describes.
//
// A deadline whose key and interval both survive keeps its phase. A changed
// interval is a different schedule as far as phase goes and starts fresh --
// carrying a deadline computed from the old interval would fire it at neither
// rate.
func (ts *Timers) Retarget(v *View) {
	if ts == nil {
		return
	}
	old := make(map[Key]*timerEntry, len(ts.entries))
	for _, e := range ts.entries {
		old[e.Key] = e
	}
	now := ts.clock.Now()
	var out []*timerEntry
	if v != nil {
		for _, mt := range v.Timers {
			e := &timerEntry{
				Key:      mt.Key,
				Interval: mt.Interval,
				Tick:     mt.Tick,
				Env:      mt.Env,
				next:     now.Add(mt.Interval),
			}
			if p, ok := old[mt.Key]; ok && p.Interval == mt.Interval {
				e.next = p.next
			}
			out = append(out, e)
		}
	}
	ts.entries = out
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
// No `enabled` gate remains here. `enabled` is the position the platform
// override writes its primitive at -- `if enabled { Tick(...) }` -- so a
// disabled timer describes no deadline and is not in this list at all. That is
// also why a re-enabled timer delivers no burst of the fires it missed: it was
// never scheduled to miss them.
func (ts *Timers) FireDue(_ *Env) (int, error) {
	now := ts.clock.Now()
	fired := 0
	for _, e := range ts.entries {
		if e.next.After(now) {
			continue
		}
		e.next = now.Add(e.Interval)
		if e.Tick == nil || e.Env == nil {
			continue
		}
		// ExecBlock rather than a raw Exec loop: a `return` in a handler ends
		// that handler, and only ExecBlock swallows the signal.
		if err := e.Env.ExecBlock(e.Tick.Block); err != nil {
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

// Keys is the mounted path of every scheduled timer, in mount order. A timer is
// keyed on where it is written, the same way an effect is, so a caller that
// wants one has to ask which are there rather than construct a positional key.
func (ts *Timers) Keys() []Key {
	out := make([]Key, 0, len(ts.entries))
	for _, e := range ts.entries {
		out = append(out, e.Key)
	}
	return out
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

// durationFromMs turns an evaluated `duration` into a Go duration. `unit
// duration` bases on ms (lib/time/time.sngl), so the number is milliseconds.
func durationFromMs(v any) (time.Duration, error) {
	ms := toFloat(v)
	if ms <= 0 {
		return 0, fmt.Errorf("interval is %v, want a positive duration", v)
	}
	return time.Duration(ms * float64(time.Millisecond)), nil
}
