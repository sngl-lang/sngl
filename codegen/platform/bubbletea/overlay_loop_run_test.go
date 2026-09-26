package bubbletea

import "testing"

// overlayLoopRunSrc opens a modal from a spliced stateful component, once per
// element: each copy's modal is gated on that copy's own cell.
const overlayLoopRunSrc = `
import . "sngl:ui"

component Item(label string) node {
    var open = false

    vbox {
        button(text=label, @click { open = true })
        modal(open=open) {
            text(value="modal {label}")
        }
    }
}

window {
    var items = ["a", "b"]

    vbox {
        for var it = items {
            Item(label=it)
        }
    }
}
`

const overlayLoopRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k rune) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: k})
	return next.(Model)
}

// The second copy's button opens the second copy's modal, which freezes the
// background, and Escape closes that copy's and no other's.
func TestACopysModalOpensAndCloses(t *testing.T) {
	m := New()
	m = key(m, tea.KeyTab)
	m = key(m, tea.KeyEnter)
	if !m.open__inst0["1"] || m.open__inst0["0"] {
		t.Fatalf("Enter on the second copy opened %v", m.open__inst0)
	}
	cursor := m.__focusLoop0_cursor
	m = key(m, tea.KeyTab)
	if m.__focusLoop0_cursor != cursor {
		t.Fatal("Tab moved focus behind an open modal")
	}
	m = key(m, tea.KeyEsc)
	if m.open__inst0["1"] {
		t.Fatalf("Escape left the modal open: %v", m.open__inst0)
	}
	m = key(m, tea.KeyTab)
	if m.__focusLoop0_cursor == cursor {
		t.Fatal("focus stayed frozen after the modal closed")
	}
}
`

func TestALoopModalOpensAndClosesPerCopy(t *testing.T) {
	runEmittedProgram(t, overlayLoopRunSrc, overlayLoopRunDriver)
}
