package bubbletea

import "testing"

const inputBlurCommitRunSrc = `
import ui "sngl:ui"

ui.window {
    var memo = ""
    var away = ""
    var changes = 0
    var committed = ""

    ui.vbox {
        ui.input(:value=memo, @change(v) {
            changes += 1
            committed = v
        })
        ui.input(:value=away)
    }
}
`

const inputBlurCommitRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k tea.Msg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

func tab(m Model) Model { return key(m, tea.KeyPressMsg{Code: tea.KeyTab}) }

// Focus starts on the first input.
func TestFocusLeavingCommitsAnEditOnce(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, tea.KeyPressMsg{Code: 'h', Text: "h"})
	if m.changes != 0 {
		t.Fatalf("typing committed: changes %d", m.changes)
	}
	m = tab(m)
	if m.changes != 1 || m.committed != "h" {
		t.Fatalf("leaving: changes %d, committed %q; want 1, \"h\"", m.changes, m.committed)
	}
	m = tab(tab(m))
	if m.changes != 1 {
		t.Fatalf("leaving unedited committed again: changes %d", m.changes)
	}
}

func TestEnterThenFocusLeavingCommitsOnce(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, tea.KeyPressMsg{Code: 'h', Text: "h"})
	m = key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = tab(m)
	if m.changes != 1 {
		t.Fatalf("Enter then leaving: changes %d, want 1", m.changes)
	}
}

func TestFocusLeavingAfterAProgramWriteCommitsNothing(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, setMemoMsg{value: "abc"})
	m = tab(m)
	if m.memo != "abc" || m.changes != 0 {
		t.Fatalf("memo %q, changes %d; want \"abc\", 0", m.memo, m.changes)
	}
}
`

func TestAnInputCommitsWhenFocusLeavesIt(t *testing.T) {
	runEmittedProgram(t, inputBlurCommitRunSrc, inputBlurCommitRunDriver)
}
