package fyne

import (
	"testing"
)

// TestATimerFiresInTheEmittedProgram builds the emitted model and runs its
// tickers.
//
// The platform harness otherwise only asks whether the Go compiles, and a timer
// that is emitted but wired to nothing compiles perfectly. The child
// component's is the one that was not emitted at all: the analysis collected
// `pkg.Timers` plus `main`'s, so a timer written anywhere else was checked,
// type-correct and inert.
func TestATimerFiresInTheEmittedProgram(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:time"

component beat(label = "") node {
    var beats = 0
    timer(interval=5ms, enabled=true, @tick { beats += 1 })
    text(value="{label} {beats}")
}

window {
    var (
        seconds = 0
        running = true
    )
    timer(interval=5ms, enabled=running, @tick { seconds += 1 })
    beat(label="b")
    text(value=string(seconds))
}
`
	runEmitted(t, "fyne-timer-run-", []byte(generateFyneModelBuilt(t, src)), `package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// A ticker runs on a goroutine and marshals its body through fyne.Do, so the
// driver needs a real Fyne app for that to be dispatched at all.
//
// Nothing arms the schedules here: the timer is an effect, so BuildUI's settle
// is what mounts them, and __snglTeardown is what would release them.
func TestTimersTick(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()
	defer m.__snglTeardown()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.Seconds() > 0 && m.Beats__inst2() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.Seconds() == 0 {
		t.Error("the root component's timer never fired")
	}
	if m.Beats__inst2() == 0 {
		t.Error("the child component's timer never fired")
	}
}
`)
}

// A schedule that has been torn down stays torn down.
//
// `time.AfterFunc` is a one-shot that re-arms itself, so `Stop` is not the end
// of it: a callback that has already fired has queued its closure through
// `fyne.Do`, and that closure runs after the unmount and would arm the next
// one. The `live` flag is what it finds instead, and both halves run on the
// thread the post hands over to, which is what makes reading it an ordering.
//
// Asserted by running rather than by a grep, because the flag is emitted either
// way -- what a revert changes is only when the ticking stops.
func TestATornDownScheduleStaysStopped(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:time"

window {
    var ticks = 0
    timer(interval=1ms, enabled=true, @tick { ticks += 1 })
    text(value=string(ticks))
}
`
	runEmitted(t, "fyne-timer-teardown-", []byte(generateFyneModelBuilt(t, src)), `package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

func TestTeardownStops(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && m.Ticks() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if m.Ticks() == 0 {
		t.Fatal("the schedule never fired")
	}

	m.__snglTeardown()
	// Long enough for a callback in flight at teardown to have been dispatched
	// and found the flag, at a 1ms period.
	time.Sleep(200 * time.Millisecond)
	settled := m.Ticks()
	time.Sleep(300 * time.Millisecond)
	if got := m.Ticks(); got != settled {
		t.Errorf("a torn-down schedule kept firing: %d then %d", settled, got)
	}
}
`)
}
