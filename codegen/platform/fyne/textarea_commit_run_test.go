package fyne

import "testing"

const textareaCommitSrc = `
import ui "sngl:ui"

var text = ""
var changes = 0

ui.window {
    ui.vbox {
        ui.textarea #notes(:value=text, @change(v) { changes += 1 })
        ui.input #other()
        ui.button #set(text="set", @click { text = "abc" })
    }
}
`

const textareaCommitDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

func TestTextareaCommitsAsTheFocusLeaves(t *testing.T) {
	test.NewApp()
	m := New()
	w := test.NewWindow(m.BuildUI())
	defer w.Close()
	c := w.Canvas()

	c.Focus(m.notes)
	test.Type(m.notes, "xy")
	m.notes.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	m.notes.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	m.notes.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	m.notes.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	if m.changes != 0 {
		t.Fatalf("typing, Enter and Shift+Enter committed %d times", m.changes)
	}
	c.Focus(m.other)
	if m.changes != 1 || m.text != "xy\n\n" {
		t.Fatalf("after an edit the focus leaving: changes = %d, text = %q; want 1, %q", m.changes, m.text, "xy\n\n")
	}
	c.Focus(m.notes)
	c.Focus(m.other)
	if m.changes != 1 {
		t.Fatalf("leaving again unedited committed: changes = %d", m.changes)
	}
	c.Focus(m.notes)
	m.set.OnTapped()
	c.Focus(m.other)
	if m.changes != 1 || m.text != "abc" {
		t.Fatalf("a program's write then leaving: changes = %d, text = %q", m.changes, m.text)
	}
	m.notesChange("q")
	if m.changes != 2 {
		t.Fatalf("the change trigger: changes = %d, want 2", m.changes)
	}
	c.Focus(m.notes)
	c.Focus(m.other)
	if m.changes != 2 {
		t.Fatalf("leaving after the change trigger committed again: changes = %d", m.changes)
	}
}
`

// A textarea commits as the focus leaves it after an edit, once; Enter and
// Shift+Enter are lines of its text, and a program's write is no edit.
func TestATextareaCommitsOnFocusLoss(t *testing.T) {
	runEmitted(t, "fyne-textarea-commit-", []byte(generateFyneModelBuilt(t, textareaCommitSrc)), textareaCommitDriver)
}
