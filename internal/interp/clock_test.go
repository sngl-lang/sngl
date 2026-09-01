package interp

import (
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/ir"
)

const twoTimersSrc = `import . "sngl:ui"
import . "sngl:time"

component main {
    var (
        fast = 0
        slow = 0
    )
    timer(interval=100ms, enabled=true, @tick { fast += 1 })
    timer(interval=500ms, enabled=true, @tick { slow += 1 })
    text(value=string(fast))
}
`

func envFor(t *testing.T, src, comp string) (*Env, *ir.Package) {
	t.Helper()
	pkg := check(t, src)
	env, err := BuildEnv(pkg, comp)
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	return env, pkg
}

func varVal(t *testing.T, env *Env, name string) any {
	t.Helper()
	for _, v := range env.Comp.Vars {
		if v.SymName() == name {
			got, _ := env.Value(v)
			return got
		}
	}
	t.Fatalf("no var %q on %s", name, env.Comp.Name)
	return nil
}

// TestTickHonoursEachTimersInterval is the behaviour change: the interpreter
// used to fire every timer once per tick, so ir.Timer.Interval was checked and
// then ignored. A tick is one timer deadline, so a 100ms timer and a 500ms
// timer advance at their declared rates.
func TestTickHonoursEachTimersInterval(t *testing.T) {
	env, _ := envFor(t, twoTimersSrc, "main")
	clock := NewVirtual()
	ts, err := NewTimers(clock, env)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}

	// Five ticks reach 500ms: the fast timer is due at each 100ms boundary,
	// the slow one only at the last.
	for i := range 5 {
		if _, err := ts.Tick(env); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if got := toFloat(varVal(t, env, "fast")); got != 5 {
		t.Errorf("fast fired %v times, want 5", got)
	}
	if got := toFloat(varVal(t, env, "slow")); got != 1 {
		t.Errorf("slow fired %v times, want 1", got)
	}
	if got := clock.Now().Sub(Epoch); got != 500*time.Millisecond {
		t.Errorf("clock advanced %v, want 500ms", got)
	}
}

// TestATickFiresEveryTimerDueAtTheSameDeadline: at 500ms both are due, and both
// must fire on that one tick.
func TestATickFiresEveryTimerDueAtTheSameDeadline(t *testing.T) {
	env, _ := envFor(t, twoTimersSrc, "main")
	ts, err := NewTimers(NewVirtual(), env)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	total := 0
	for i := range 5 {
		n, err := ts.Tick(env)
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		total += n
	}
	if total != 6 {
		t.Errorf("6 fires expected across 5 ticks (5 fast + 1 slow), got %d", total)
	}
}

// TestVirtualClockStartsAtAFixedEpoch: a snapshot that interpolates a date must
// not depend on when the suite ran.
func TestVirtualClockStartsAtAFixedEpoch(t *testing.T) {
	if got := NewVirtual().Now(); !got.Equal(Epoch) {
		t.Errorf("a fresh Virtual reads %v, want %v", got, Epoch)
	}
}

// TestRebaseCarriesPhaseForAnUnchangedInterval: reloading a file must not reset
// every timer in the program.
func TestRebaseCarriesPhaseForAnUnchangedInterval(t *testing.T) {
	env, _ := envFor(t, twoTimersSrc, "main")
	clock := NewVirtual()
	before, err := NewTimers(clock, env)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	if _, err := before.Tick(env); err != nil { // clock now at 100ms
		t.Fatalf("tick: %v", err)
	}
	wantNext, _ := before.Next()

	// A reload: fresh IR, fresh schedule, same source.
	env2, _ := envFor(t, twoTimersSrc, "main")
	after, err := NewTimers(clock, env2)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	after.Rebase(before)

	if gotNext, _ := after.Next(); !gotNext.Equal(wantNext) {
		t.Errorf("next fire after reload is %v, want the pre-reload %v", gotNext, wantNext)
	}
}

// TestRebaseReschedulesAChangedInterval: a deadline computed from the old
// interval would fire the timer at neither rate.
func TestRebaseReschedulesAChangedInterval(t *testing.T) {
	env, _ := envFor(t, twoTimersSrc, "main")
	clock := NewVirtual()
	before, err := NewTimers(clock, env)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	if _, err := before.Tick(env); err != nil {
		t.Fatalf("tick: %v", err)
	}

	edited := `import . "sngl:ui"
import . "sngl:time"

component main {
    var (
        fast = 0
        slow = 0
    )
    timer(interval=2s, enabled=true, @tick { fast += 1 })
    timer(interval=500ms, enabled=true, @tick { slow += 1 })
    text(value=string(fast))
}
`
	env2, _ := envFor(t, edited, "main")
	after, err := NewTimers(clock, env2)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	freshChanged, _ := after.NextFor(TimerKey("main", 0))
	after.Rebase(before)

	// The retimed one is scheduled fresh: a deadline computed from the old
	// interval would fire it at neither rate.
	if got, _ := after.NextFor(TimerKey("main", 0)); !got.Equal(freshChanged) {
		t.Errorf("retimed timer kept a carried deadline %v, want the fresh %v", got, freshChanged)
	}
	// Its untouched neighbour still carries, in the same reload.
	wantCarried, _ := before.NextFor(TimerKey("main", 1))
	if got, _ := after.NextFor(TimerKey("main", 1)); !got.Equal(wantCarried) {
		t.Errorf("untouched timer was rescheduled to %v, want the carried %v", got, wantCarried)
	}
}

// TestDisabledTimerDoesNotAccumulateMissedFires: `enabled` in lib/time is a
// gate, not a pause, so re-enabling must not deliver a burst.
func TestDisabledTimerDoesNotAccumulateMissedFires(t *testing.T) {
	src := `import . "sngl:ui"
import . "sngl:time"

component main {
    var (
        n = 0
        on = false
    )
    timer(interval=100ms, enabled=on, @tick { n += 1 })
    text(value=string(n))
}
`
	env, _ := envFor(t, src, "main")
	ts, err := NewTimers(NewVirtual(), env)
	if err != nil {
		t.Fatalf("NewTimers: %v", err)
	}
	for i := range 10 {
		if _, err := ts.Tick(env); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if got := toFloat(varVal(t, env, "n")); got != 0 {
		t.Fatalf("disabled timer fired %v times", got)
	}

	// Enable it, then one tick: exactly one fire, not the ten it skipped.
	for _, v := range env.Comp.Vars {
		if v.SymName() == "on" {
			env.Set(v, true)
		}
	}
	if _, err := ts.Tick(env); err != nil {
		t.Fatalf("tick after enable: %v", err)
	}
	if got := toFloat(varVal(t, env, "n")); got != 1 {
		t.Errorf("after enabling, fired %v times, want 1", got)
	}
}
