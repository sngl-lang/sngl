package bubbletea

import "testing"

const focusPrevRunSrc = `
import . "sngl:ui"

var picked = ""
var xs = ["a", "b", "c"]

window {
    vbox {
        button(text="top", @click { picked = "top" })
        for var x = xs {
            button(text=x, @click { picked = x })
        }
    }
}
`

const focusPrevRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, msg tea.KeyPressMsg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

// Shift-Tab from the stop before a loop enters it at its last node, as Tab
// out of its last node leaves it.
func TestShiftTabEntersALoopAtItsEnd(t *testing.T) {
	m := New()
	m = key(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.picked != "c" {
		t.Fatalf("Shift-Tab from the first stop landed on %q, want the loop's last, c", m.picked)
	}
}
`

func TestFocusPrevEntersALoopAtItsEnd(t *testing.T) {
	runEmittedProgram(t, focusPrevRunSrc, focusPrevRunDriver)
}
