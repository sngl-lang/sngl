package bubbletea

import "testing"

const textareaCommitRunSrc = `
import ui "sngl:ui"

ui.window {
    var memo = ""
    var away = ""
    var changes = 0
    var committed = ""

    ui.vbox {
        ui.textarea(:value=memo, @change(v) {
            changes += 1
            committed = v
        })
        ui.input(:value=away)
    }
}
`

const textareaCommitRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k tea.Msg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

func tab(m Model) Model { return key(m, tea.KeyPressMsg{Code: tea.KeyTab}) }

// Focus starts on the textarea. Enter is a line of its text; focus leaving
// commits an edit once, and leaving again unedited commits nothing.
func TestFocusLeavingCommitsAnEditOnce(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, tea.KeyPressMsg{Code: 'h', Text: "h"})
	m = key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.memo != "h\n" || m.changes != 0 {
		t.Fatalf("typing: memo %q, changes %d; want \"h\\n\", 0", m.memo, m.changes)
	}
	m = tab(m)
	if m.changes != 1 || m.committed != "h\n" {
		t.Fatalf("leaving: changes %d, committed %q; want 1, \"h\\n\"", m.changes, m.committed)
	}
	m = tab(tab(m))
	if m.changes != 1 {
		t.Fatalf("leaving unedited committed again: changes %d", m.changes)
	}
}

// The program's write, made while the textarea has focus, is no edit.
func TestFocusLeavingAfterAProgramWriteCommitsNothing(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, setMemoMsg{value: "abc"})
	m = tab(m)
	if m.memo != "abc" || m.changes != 0 {
		t.Fatalf("memo %q, changes %d; want \"abc\", 0", m.memo, m.changes)
	}
}
`

func TestATextareaCommitsWhenFocusLeavesIt(t *testing.T) {
	runEmittedProgram(t, textareaCommitRunSrc, textareaCommitRunDriver)
}
