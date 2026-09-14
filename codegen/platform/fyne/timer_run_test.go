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
		if m.Seconds() > 0 && m.Beats__inst0() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.Seconds() == 0 {
		t.Error("the root component's timer never fired")
	}
	if m.Beats__inst0() == 0 {
		t.Error("the child component's timer never fired")
	}
}
`)
}
