package bubbletea

import "testing"

// windowCloseRunSrc is a window whose `@closed` keeps it on screen the first
// time: ctrl+c is the window's close on a terminal, reported as
// `visible = false` before the handler runs, and the program ends only when the
// window is still off screen after it.
const windowCloseRunSrc = `
import time "sngl:time"
import ui "sngl:ui"

var (
    shown = true
    closes = 0
    ticks = 0
)

ui.window #main(title="Main", :visible=shown, @closed {
    closes += 1
    if closes < 2 {
        main.open()
    }
}) {
    ui.text(value="closed {closes} times")
    time.timer(interval=10ms, @tick {
        ticks += 1
    })
    ui.button(text="Hide", @click {
        main.close()
    })
}
`

const windowCloseRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func ctrlC(m Model) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	return next.(Model), cmd
}

// The first close is answered by the handler reopening the window, so the
// program keeps running; the second is not, and it ends.
func TestCtrlCIsTheWindowsClose(t *testing.T) {
	m := New()
	m, cmd := ctrlC(m)
	if quits(cmd) {
		t.Fatal("quit on a close the handler answered by reopening the window")
	}
	if m.closes != 1 || !m.shown {
		t.Fatalf("closes=%d shown=%v after the first close, want 1 true", m.closes, m.shown)
	}
	if !strings.Contains(m.View().Content, "closed 1 times") {
		t.Fatalf("view after the first close: %q", m.View().Content)
	}
	m, cmd = ctrlC(m)
	if m.closes != 2 || m.shown {
		t.Fatalf("closes=%d shown=%v after the second close, want 2 false", m.closes, m.shown)
	}
	if !quits(cmd) {
		t.Fatal("the second close left the window off screen and did not quit")
	}
}

// close() takes the window off screen without ending the program: a hidden
// window draws nothing, and what it holds lives on -- its timer still ticks.
func TestAHiddenWindowDrawsNothing(t *testing.T) {
	m := New()
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if quits(cmd) {
		t.Fatal("close() quit the program")
	}
	if m.shown {
		t.Fatal("close() left shown true")
	}
	if strings.Contains(m.View().Content, "closed") {
		t.Fatalf("a hidden window drew %q", m.View().Content)
	}
	m.__timer0Sync()
	next, _ = m.Update(timerTickMsg0{key: "", gen: m.__timer0[""]})
	m = next.(Model)
	if m.ticks != 1 {
		t.Fatalf("a hidden window's timer did not tick: ticks=%d", m.ticks)
	}
}
`

func TestCtrlCClosesTheWindowInTheEmittedProgram(t *testing.T) {
	runEmittedProgram(t, windowCloseRunSrc, windowCloseRunDriver)
}
