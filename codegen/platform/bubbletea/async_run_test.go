package bubbletea

import "testing"

// asyncRunSrc starts work off the event loop that waits to be let go, then
// reports the state it reads, while a later click writes that state; and
// keeps a closure in state that a later click calls.
const asyncRunSrc = `
import async "sngl:async"
import go "sngl:language/go"
import ui "sngl:ui"

#[go.native("duckfam.us/sngl/codegen/platform/bubbletea/internal/asynchook", "asynchook.Wait")]
func wait()

#[go.native("duckfam.us/sngl/codegen/platform/bubbletea/internal/asynchook", "asynchook.Report")]
func report(v int)

var (
    n = 1
    xs list[int] = [1]
    seen = 0
    later func() = func() {}
)

ui.window(title="Async") {
    ui.button(text="read", @click {
        async.start(func() {
            wait()
            report(n + xs[0])
        })
    })
    ui.button(text="set", @click {
        n = 5
    })
    ui.button(text="bump", @click {
        n = 9
        xs[0] = 9
    })
    ui.button(text="arm", @click {
        later = func() {
            seen = n
        }
    })
    ui.button(text="fire", @click {
        later()
    })
}
`

const asyncRunDriver = `package ui

import (
	"testing"
	"time"

	"charm.land/bubbletea/v2"

	"duckfam.us/sngl/codegen/platform/bubbletea/internal/asynchook"
)

func press(m Model, focus int) Model {
	m.__focusID = focus
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return nm.(Model)
}

// Work a click started reads the state as it was when it started, while an
// Update after it writes that state -- a list element included -- and the
// two race on nothing.
func TestStartedWorkReadsTheStateItStartedWith(t *testing.T) {
	m := press(New(), 1)
	m = press(m, 0)
	m = press(m, 2)
	asynchook.Release()
	select {
	case v := <-asynchook.Reported():
		if v != 6 {
			t.Fatalf("the work read n + xs[0] = %d, want the 6 it started with", v)
		}
		if m.n != 9 || m.xs[0] != 9 {
			t.Fatalf("the later Update's writes were lost: n=%d xs[0]=%d", m.n, m.xs[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the work never reported")
	}
}

// A closure kept in state and called by a later Update writes the state
// that Update hands back.
func TestAKeptClosureWritesTheLiveState(t *testing.T) {
	m := press(New(), 3)
	m.n = 7
	m = press(m, 4)
	if m.seen != 7 {
		t.Fatalf("seen=%d, want 7", m.seen)
	}
}
`

// TestStartedWorkAndKeptClosuresInTheEmittedProgram runs the emitted program
// under the race detector.
func TestStartedWorkAndKeptClosuresInTheEmittedProgram(t *testing.T) {
	runEmittedProgram(t, asyncRunSrc, asyncRunDriver, "-race")
}
