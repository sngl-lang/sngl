package fyne

import (
	"strings"
	"testing"
)

// asyncOffloadSrc: a click that waits, then reads a value, then puts both on
// the screen. `sleep` is a call statement with nothing to hand back and
// `host` is an assignment with an answer, which are the two shapes the split
// has to handle -- and `busy` on either side of them is what says which
// statements stayed on the drawing thread.
const asyncOffloadSrc = `
import . "sngl:ui"
import go "sngl:language/go"

#[go.native("time", "time.Sleep")]
#[go.async]
func sleep(ns int)

#[go.native("os", "os.Hostname", fails)]
#[go.async]
func host() string

window {
    var (
        greeting = "idle"
        busy = false
    )

    text #out(value=greeting)
    button #load(text="load", @click {
        busy = true
        sleep(300000000)
        greeting = host()
        busy = false
    })
}
`

// asyncOffloadDriver is the assertion the whole mechanism exists for: the
// click returns while the work is still running.
//
// Without the offload OnTapped() would not come back for 300ms and `greeting`
// would already be set when it did -- which is exactly what a frozen window
// is. The elapsed time is checked as well as the state, because a state
// assertion alone would still pass if the call had blocked and simply been
// fast.
const asyncOffloadDriver = `package ui

import (
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestTheClickReturnsWhileTheWorkRuns(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	start := time.Now()
	m.load.OnTapped()
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("the click took %v; the blocking call ran on the thread that draws", elapsed)
	}
	if !m.busy {
		t.Errorf("busy = false right after the click; the statement before the blocking call did not run where it was written")
	}
	if m.greeting != "idle" {
		t.Errorf("greeting = %q right after the click; the answer was written before it could have arrived", m.greeting)
	}

	// Now let it land. fyne.Do queues onto the driver's goroutine, so the
	// posted closure runs only once something pumps it.
	want, _ := os.Hostname()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		done := make(chan bool, 1)
		fyne.Do(func() { done <- m.greeting == want && !m.busy })
		if <-done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the answer never arrived: greeting = %q busy = %v, want %q false", m.greeting, m.busy, want)
}
`

// TestABlockingCallDoesNotHoldTheClick is the executable half of #[go.async]:
// a string in model.go cannot say whether the interface stayed responsive, so
// the emitted program is compiled and run and asked.
func TestABlockingCallDoesNotHoldTheClick(t *testing.T) {
	runEmitted(t, "fyne-async-offload-", []byte(generateFyneModelBuilt(t, asyncOffloadSrc)), asyncOffloadDriver)
}

// asyncOffloadWindowSrc writes the same handler inside a `window` a component
// renders. A component that renders windows names `root`, the family a
// file's root accepts — a `ui` component is not a place a window belongs.
const asyncOffloadWindowSrc = `
import . "sngl:ui"
import go "sngl:language/go"

#[go.native("time", "time.Sleep")]
#[go.async]
func sleep(ns int)

component pages() root {
    var busy = false

    window(title="probe") {
        button #load(text="load", @click {
            busy = true
            sleep(300000000)
            busy = false
        })
    }
}
`

// A window a component renders owns funcs of its own, and that is where the
// flattened handler lands. Reading pkg.Windows alone offloaded the handler in
// one spelling and left the other blocking on the drawing thread, with no
// diagnostic either way: the program compiled and the window froze.
//
// passRootWindow lifts a root component's windows onto pkg.Windows, so the two
// spellings are one list by the time a backend sees them -- which is why this
// asserts the outcome and not the route to it.
//
// The claim stops at the shape rather than a compile, because this spelling
// does not compile for a reason of its own: fyne emits the handler as
// `m.load.OnTapped = load_click_handler`, a method value with no receiver, for
// any handler on a widget inside a component-rendered window -- with or
// without a blocking call in it.
func TestAHandlerInsideAComponentsWindowOffloadsToo(t *testing.T) {
	model := generateFyneModelBuilt(t, asyncOffloadWindowSrc)
	if !strings.Contains(model, "go func() {") || !strings.Contains(model, "fyne.Do(func() {") {
		t.Errorf("the handler still blocks the drawing thread\n--- model.go ---\n%s", model)
	}
}

// asyncFuncvarSrc calls the blocking function through a state var rather than
// by name. The call names no declaration at all -- the callee is the variable
// -- so IsAsync is not there to read, and the only record of what the call may
// reach is the slot colour the checker's points-to analysis left behind.
// Asking for the flag alone emitted `m.greeting = m.handler()` straight onto
// the drawing thread, with no goroutine and no diagnostic: the outcome the
// mark exists to prevent, in the one spelling nothing was watching.
const asyncFuncvarSrc = `
import . "sngl:ui"
import go "sngl:language/go"

#[go.native("os", "os.Hostname", fails)]
#[go.async]
func host() string

window {
    var (
        handler func() string = host
        greeting = "idle"
    )

    text #out(value=greeting)
    button #load(text="load", @click {
        greeting = handler()
    })
}
`

// The claim stops at the shape rather than a compile, for the same kind of
// reason TestAHandlerInsideAComponentsWindowOffloadsToo does: this spelling
// does not build on fyne for a defect of its own, with or without a blocking
// call in it. `var handler func() string = host` emits `m.handler = m.host()`
// -- the Go backend calls the function where it should take a reference to it
// -- and the program is rejected by the Go compiler before any of this runs.
// That is a separate bug about funcvar initialisation and is not fixed here;
// what is asserted here is that the offload sees through the funcvar, which is
// the half that was silent.
func TestABlockingCallThroughAFuncvarLeavesTheThreadToo(t *testing.T) {
	model := generateFyneModelBuilt(t, asyncFuncvarSrc)
	if !strings.Contains(model, "go func() {") || !strings.Contains(model, "fyne.Do(func() {") {
		t.Errorf("the call through the funcvar still runs on the drawing thread\n--- model.go ---\n%s", model)
	}
}
