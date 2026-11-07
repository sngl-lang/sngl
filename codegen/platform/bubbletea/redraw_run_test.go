package bubbletea

import "testing"

// sleepRunSrc holds a spinner under a branch, which ticks only while drawn.
const sleepRunSrc = `
import ui "sngl:ui"

var busy = false

ui.window {
    ui.vbox {
        if busy {
            ui.spinner()
        }
        ui.text(value="idle")
    }
}
`

const sleepRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbletea/v2"
)

// ticks runs cmd, a batch or one command, and says whether any of what it
// produces is a spinner's tick. A command blocking on the wake is not run.
func ticks(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if c != nil && ticks(t, c) {
				return true
			}
		}
	case spinner.TickMsg:
		return true
	}
	return false
}

// A spinner the view does not draw stops ticking, and the view drawing it
// again wakes it with a tick of its own.
func TestASpinnerNotDrawnSleeps(t *testing.T) {
	m := New()
	_ = m.View()
	next, cmd := m.Update(m.widget0.Tick())
	m = next.(Model)
	if ticks(t, cmd) {
		t.Fatal("a spinner not drawn asked for another tick")
	}
	select {
	case <-m.__wake:
		t.Fatal("the view woke a spinner it did not draw")
	default:
	}
	m.busy = true
	_ = m.View()
	select {
	case <-m.__wake:
	default:
		t.Fatal("drawing the sleeping spinner did not wake it")
	}
	next, cmd = m.Update(snglWakeMsg{})
	m = next.(Model)
	_ = m.View()
	tick := m.widget0.Tick()
	if cmd == nil {
		t.Fatal("waking answered nothing")
	}
	next, cmd = m.Update(tick)
	if !ticks(t, cmd) {
		t.Fatal("a drawn spinner stopped ticking")
	}
}
`

func TestSpinnersSleepWhileNotDrawn(t *testing.T) {
	runEmittedProgram(t, sleepRunSrc, sleepRunDriver)
}

// drainRunSrc posts back to the loop, so the program listens for posts.
const drainRunSrc = `
import async "sngl:async"
import ui "sngl:ui"

var n = 0

ui.window {
    ui.button(text="go", @click { async.start(func() { async.do(func() { n += 1 }) }) })
}
`

const drainRunDriver = `package ui

import "testing"

// One message runs every closure posted ahead of it: each message is a draw,
// and answers arriving together are drawn once.
func TestPostsArrivingTogetherAreOneUpdate(t *testing.T) {
	m := New()
	for range 4 {
		__snglPost(func() { m.n += 1 })
	}
	next, _ := m.Update(__snglAwaitPost())
	m = next.(Model)
	if m.n != 4 {
		t.Fatalf("one Update ran %d of 4 posts", m.n)
	}
}
`

func TestPostsArrivingTogetherDrawOnce(t *testing.T) {
	runEmittedProgram(t, drainRunSrc, drainRunDriver)
}
