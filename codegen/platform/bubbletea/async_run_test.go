package bubbletea

import "testing"

// asyncRunSrc starts work off the event loop that reads the program's state,
// and keeps a closure in state that a later click calls.
const asyncRunSrc = `
import async "sngl:async"
import ui "sngl:ui"

var (
    n = 1
    seen = 0
    later func() = func() {}
)

func peek() {
    if n < 0 {
        n = 0
    }
}

ui.window(title="Async") {
    ui.button(text="read", @click {
        async.start(func() {
            peek()
        })
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
)

func press(m Model, focus int) Model {
	m.__focusID = focus
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return nm.(Model)
}

// A goroutine started by a click reads the state the program holds; read
// through a copy of the Model, or anything one Update sets and the next
// clears, it read nothing at all.
func TestStartedWorkReadsTheState(t *testing.T) {
	m := press(New(), 0)
	time.Sleep(50 * time.Millisecond)
	if m.n != 1 {
		t.Fatalf("n=%d, want 1", m.n)
	}
}

// A closure kept in state and called by a later Update writes the state
// that Update hands back.
func TestAKeptClosureWritesTheLiveState(t *testing.T) {
	m := press(New(), 1)
	m.n = 7
	m = press(m, 2)
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
