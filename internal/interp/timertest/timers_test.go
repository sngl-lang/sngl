// Package timertest holds the interpreter's timer tests.
//
// They are here rather than in internal/interp because a timer is no longer a
// compiler construct: `sngl:time`'s `timer` has no body of its own, and the
// schedule comes from `sngl:platform/none`'s override of it. Checking a program
// that names one therefore needs the platform registered -- and interp's own
// tests are internal (`package interp`), so importing the registry there closes
// a cycle through internal/interp/testrunner, which imports interp.
package timertest

import (
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
	"git.duckfam.us/jonathan/sngl/ir"
)

const twoTimersSrc = `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        fast = 0
        slow = 0
    )
    timer(interval=100ms, enabled=true, @tick { fast += 1 })
    timer(interval=500ms, enabled=true, @tick { slow += 1 })
    text(value=string(fast))
}
`

func check(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	langs, plats := testtargets.Targets()
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Languages: langs,
		Platforms: plats,
		Targets:   []ir.StaticTarget{{Platform: "none"}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	return pkg
}

// envFor builds the scope, with the interpreter's own platform bodies swapped
// in -- the same thing interp.NewSession does, and the reason a timer schedules
// anything at all.
func envFor(t *testing.T, src, comp string) *interp.Env {
	t.Helper()
	pkg := check(t, src)
	ir.SpecializeForTarget(pkg, interp.InterpreterPlatform, "")
	env, err := interp.BuildEnv(pkg, comp)
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	return env
}

// schedFor is the schedule env's component currently describes. A timer is a
// node, so the deadlines are whatever the tree holds; there is no list on the
// declaration to read.
func schedFor(t *testing.T, clock interp.Clock, env *interp.Env) *interp.Timers {
	t.Helper()
	ts := interp.NewTimers(clock)
	ts.Retarget(mountOf(t, env))
	return ts
}

func mountOf(t *testing.T, env *interp.Env) *interp.View {
	t.Helper()
	v, err := interp.Mount(env)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return v
}

func varVal(t *testing.T, env *interp.Env, name string) float64 {
	t.Helper()
	for _, v := range env.Comp.Vars {
		if v.SymName() == name {
			got, _ := env.Value(v)
			f, ok := got.(float64)
			if !ok {
				if n, isInt := got.(int); isInt {
					return float64(n)
				}
				t.Fatalf("var %q holds %T, want a number", name, got)
			}
			return f
		}
	}
	t.Fatalf("no var %q on %s", name, env.Comp.Name)
	return 0
}

// TestTickHonoursEachTimersInterval: a tick is one timer deadline, not one fire
// of everything, so a 100ms timer and a 500ms timer advance at their declared
// rates.
func TestTickHonoursEachTimersInterval(t *testing.T) {
	env := envFor(t, twoTimersSrc, "main")
	clock := interp.NewVirtual()
	ts := schedFor(t, clock, env)

	// Five ticks reach 500ms: the fast timer is due at each 100ms boundary,
	// the slow one only at the last.
	for i := range 5 {
		if _, err := ts.Tick(env); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if got := varVal(t, env, "fast"); got != 5 {
		t.Errorf("fast fired %v times, want 5", got)
	}
	if got := varVal(t, env, "slow"); got != 1 {
		t.Errorf("slow fired %v times, want 1", got)
	}
	if got := clock.Now().Sub(interp.Epoch); got != 500*time.Millisecond {
		t.Errorf("clock advanced %v, want 500ms", got)
	}
}

// TestATickFiresEveryTimerDueAtTheSameDeadline: at 500ms both are due, and both
// must fire on that one tick.
func TestATickFiresEveryTimerDueAtTheSameDeadline(t *testing.T) {
	env := envFor(t, twoTimersSrc, "main")
	ts := schedFor(t, interp.NewVirtual(), env)
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

// TestRetargetCarriesPhaseForAnUnchangedInterval: re-deriving the schedule --
// which every tick and every reload does -- must not reset a deadline.
func TestRetargetCarriesPhaseForAnUnchangedInterval(t *testing.T) {
	env := envFor(t, twoTimersSrc, "main")
	clock := interp.NewVirtual()
	ts := schedFor(t, clock, env)
	if _, err := ts.Tick(env); err != nil { // clock now at 100ms
		t.Fatalf("tick: %v", err)
	}
	wantNext, _ := ts.Next()

	// Off the interval boundary, or the test cannot tell a carried deadline
	// from a fresh one: at 100ms a fresh schedule computes 200ms too, which is
	// exactly what carrying would have produced. At 130ms they differ.
	clock.Advance(30 * time.Millisecond)
	ts.Retarget(mountOf(t, env))

	if gotNext, _ := ts.Next(); !gotNext.Equal(wantNext) {
		t.Errorf("next fire moved to %v from %v; re-deriving restarted the timer", gotNext, wantNext)
	}
}

// TestRetargetReschedulesAChangedInterval: a deadline computed from the old
// interval would fire the timer at neither rate.
func TestRetargetReschedulesAChangedInterval(t *testing.T) {
	env := envFor(t, twoTimersSrc, "main")
	clock := interp.NewVirtual()
	ts := schedFor(t, clock, env)
	if _, err := ts.Tick(env); err != nil {
		t.Fatalf("tick: %v", err)
	}
	clock.Advance(30 * time.Millisecond)
	// The neighbour's own deadline, not the earliest of the two: the fast timer
	// is the earliest, and it is the one being retimed.
	before := ts.Keys()
	if len(before) != 2 {
		t.Fatalf("the schedule holds %d timers, want 2", len(before))
	}
	carried, _ := ts.NextFor(before[1])

	edited := envFor(t, `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        fast = 0
        slow = 0
    )
    timer(interval=2s, enabled=true, @tick { fast += 1 })
    timer(interval=500ms, enabled=true, @tick { slow += 1 })
    text(value=string(fast))
}
`, "main")
	ts.Retarget(mountOf(t, edited))

	keys := ts.Keys()
	if len(keys) != 2 {
		t.Fatalf("the reloaded schedule holds %d timers, want 2", len(keys))
	}
	// The retimed one starts fresh at now+2s; its untouched neighbour carries.
	if got, _ := ts.NextFor(keys[0]); !got.After(clock.Now().Add(time.Second)) {
		t.Errorf("retimed timer fires at %v, which is not a fresh 2s deadline", got)
	}
	if got, _ := ts.NextFor(keys[1]); !got.Equal(carried) {
		t.Errorf("untouched timer was rescheduled to %v, want the carried %v", got, carried)
	}
}

// TestADisabledTimerDescribesNoDeadline: `enabled` is the position the override
// places its primitive at, so a disabled timer is not scheduled at all -- which
// is also why re-enabling delivers no burst of the fires it "missed".
func TestADisabledTimerDescribesNoDeadline(t *testing.T) {
	env := envFor(t, `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        n = 0
        on = false
    )
    timer(interval=100ms, enabled=on, @tick { n += 1 })
    text(value=string(n))
}
`, "main")
	ts := schedFor(t, interp.NewVirtual(), env)
	if len(ts.Keys()) != 0 {
		t.Fatalf("a disabled timer described %d deadlines", len(ts.Keys()))
	}
	for i := range 10 {
		if _, err := ts.Tick(env); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if got := varVal(t, env, "n"); got != 0 {
		t.Fatalf("disabled timer fired %v times", got)
	}

	for _, v := range env.Comp.Vars {
		if v.SymName() == "on" {
			env.Set(v, true)
		}
	}
	ts.Retarget(mountOf(t, env))
	if _, err := ts.Tick(env); err != nil {
		t.Fatalf("tick after enable: %v", err)
	}
	if got := varVal(t, env, "n"); got != 1 {
		t.Errorf("after enabling, fired %v times, want 1", got)
	}
}

const sessionSrc = `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        count = 0
        label = "start"
    )
    vbox {
        text #out(value="{label}: {count}")
        timer(interval=100ms, enabled=true, @tick { count += 10 })
    }
}
`

// TestTickAdvancesTimeAndPatches is the loop a window runs on a schedule: the
// clock reaches a deadline, the handler moves state, and what changed comes
// back as patches.
func TestTickAdvancesTimeAndPatches(t *testing.T) {
	s, err := interp.NewSession(check(t, sessionSrc), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	patches, err := s.Tick()
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(patches) == 0 {
		t.Fatal("a tick that moved state produced no patches")
	}
	out := s.View().Find("out")
	if len(out) != 1 {
		t.Fatalf("#out resolved to %d nodes", len(out))
	}
	if got := out[0].Props["value"]; got != "start: 10" {
		t.Errorf("after one tick #out reads %q, want %q", got, "start: 10")
	}
}

// TestReloadDoesNotRestartTimerPhase: saving a file must not reset every timer
// in the program.
func TestReloadDoesNotRestartTimerPhase(t *testing.T) {
	s, err := interp.NewSession(check(t, sessionSrc), "main", interp.NewVirtual())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := s.Tick(); err != nil { // clock at 100ms, timer due at 200ms
		t.Fatalf("Tick: %v", err)
	}
	// Off the interval boundary, or a carried deadline and a fresh one agree.
	s.Clock.(*interp.Virtual).Advance(30 * time.Millisecond)

	keys := s.Timers.Keys()
	if len(keys) != 1 {
		t.Fatalf("the session holds %d timers, want 1", len(keys))
	}
	want, ok := s.Timers.NextFor(keys[0])
	if !ok {
		t.Fatal("no timer scheduled")
	}
	edited := sessionSrc[:0] + `import . "sngl:ui"
import . "sngl:time"

component main node {
    var (
        count = 0
        label = "restarted"
    )
    vbox {
        text #out(value="{label}: {count}")
        timer(interval=100ms, enabled=true, @tick { count += 10 })
    }
}
`
	if _, err := s.Reload(check(t, edited)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	keys = s.Timers.Keys()
	if len(keys) != 1 {
		t.Fatalf("after the reload the session holds %d timers, want 1", len(keys))
	}
	got, ok := s.Timers.NextFor(keys[0])
	if !ok {
		t.Fatal("the timer is gone after the reload")
	}
	if !got.Equal(want) {
		t.Errorf("next fire moved to %v from %v; the reload restarted the timer", got, want)
	}
}
