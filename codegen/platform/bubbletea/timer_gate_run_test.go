package bubbletea

import "testing"

// timerGateRunSrc is a timer whose gate is not a bare state read: its own
// `enabled` is an expression, and the `if` it is written under is folded onto
// it. Both used to reduce to no gate at all, so the timer ran while the branch
// was off, and a gate that did stop a timer never re-armed it when it came
// back on.
const timerGateRunSrc = `
import time "sngl:time"
import ui "sngl:ui"

var (
    on = true
    ticks = 0
)

ui.window(title="Gate") {
    ui.text(value="ticks {ticks}")
    if on {
        time.timer(interval=1ms, enabled=ticks < 100, @tick {
            ticks += 1
        })
    }
    ui.button(text="Toggle", @click {
        on = !on
    })
}
`

const timerGateRunDriver = `package ui

import (
	"testing"
	"time"

	"charm.land/bubbletea/v2"
)

// pending runs cmd and everything it batches, and returns the messages that
// came back within a deadline -- the ticks it armed, named by nothing here, so
// the driver holds the program to what it schedules rather than to how.
func pending(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(500 * time.Millisecond):
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, pending(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func deliver(m Model, msgs []tea.Msg) (Model, []tea.Msg) {
	var next []tea.Msg
	for _, msg := range msgs {
		nm, cmd := m.Update(msg)
		m = nm.(Model)
		next = append(next, pending(cmd)...)
	}
	return m, next
}

func toggle(m Model) (Model, []tea.Msg) {
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return nm.(Model), pending(cmd)
}

func TestATimerUnderAnIfStopsAndStartsWithIt(t *testing.T) {
	m := New()
	m, armed := deliver(m, pending(m.Init()))
	if m.ticks != 1 {
		t.Fatalf("ticks=%d after the first tick, want 1", m.ticks)
	}

	m, _ = toggle(m)
	if m.on {
		t.Fatal("the button did not turn the branch off")
	}
	m, armed = deliver(m, armed)
	m, armed = deliver(m, armed)
	if m.ticks != 1 {
		t.Fatalf("the timer ticked while its branch was off: ticks=%d", m.ticks)
	}

	m, armed = toggle(m)
	m, _ = deliver(m, armed)
	if m.ticks != 2 {
		t.Fatalf("the timer did not start again with its branch: ticks=%d, want 2", m.ticks)
	}
}
`

func TestATimerGateIsAnExpressionInTheEmittedProgram(t *testing.T) {
	runEmittedProgram(t, timerGateRunSrc, timerGateRunDriver)
}
