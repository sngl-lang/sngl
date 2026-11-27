package fyne

import "testing"

const inputBlurCommitSrc = `
import ui "sngl:ui"

var text = ""
var changes = 0
var order = ""

ui.window {
    ui.vbox {
        ui.input #field(:value=text, @change(v) {
            changes += 1
            order = order + "c"
        }, @submit { order = order + "s" })
        ui.input #other()
        ui.button #set(text="set", @click { text = "abc" })
    }
}
`

const inputBlurCommitDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestInputCommitsAsTheFocusLeaves(t *testing.T) {
	test.NewApp()
	m := New()
	w := test.NewWindow(m.BuildUI())
	defer w.Close()
	c := w.Canvas()

	c.Focus(m.field)
	test.Type(m.field, "xy")
	c.Focus(m.other)
	if m.changes != 1 || m.text != "xy" {
		t.Fatalf("after an edit the focus leaving: changes = %d, text = %q; want 1, %q", m.changes, m.text, "xy")
	}
	c.Focus(m.field)
	c.Focus(m.other)
	if m.changes != 1 {
		t.Fatalf("leaving again unedited committed: changes = %d", m.changes)
	}
	c.Focus(m.field)
	test.Type(m.field, "z")
	m.field.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	c.Focus(m.other)
	if m.changes != 2 || m.order != "ccs" {
		t.Fatalf("Enter then leaving: changes = %d, order = %q; want 2, %q", m.changes, m.order, "ccs")
	}
	c.Focus(m.field)
	m.set.OnTapped()
	c.Focus(m.other)
	if m.changes != 2 || m.text != "abc" {
		t.Fatalf("a program's write then leaving: changes = %d, text = %q", m.changes, m.text)
	}
}
`

// An input commits an edit as the focus leaves it, through the mark its
// Enter clears, so Enter then leaving commits once.
func TestAnInputCommitsOnFocusLoss(t *testing.T) {
	runEmitted(t, "fyne-input-blur-", []byte(generateFyneModelBuilt(t, inputBlurCommitSrc)), inputBlurCommitDriver)
}
