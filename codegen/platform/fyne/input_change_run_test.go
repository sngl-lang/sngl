package fyne

import "testing"

const inputChangeSrc = `
import . "sngl:ui"

var last = ""

window {
    vbox {
        input #box(@change(e) { last = "got " + e })
        select #pick(@change(e) { last = "sel " + e }) {
            choice(label="x", value="x")
            choice(label="y", value="y")
        }
    }
}
`

const inputChangeDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestChangeCarriesTheCommittedValue(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()
	test.Type(m.box, "hello")
	m.box.OnSubmitted(m.box.Text)
	if m.last != "got hello" {
		t.Fatalf("after typing and submitting the entry last = %q, want %q", m.last, "got hello")
	}
	m.last = ""
	m.box.OnSubmitted(m.box.Text)
	if m.last != "" {
		t.Fatalf("submitting it again unedited committed %q", m.last)
	}
	m.pick.SetSelected("y")
	if m.last != "sel y" {
		t.Fatalf("after selecting last = %q, want %q", m.last, "sel y")
	}
}
`

// An input's @change fires on the commit of an edit with the committed text,
// the value the interpreter gives.
func TestAnInputChangeRuns(t *testing.T) {
	runEmitted(t, "fyne-input-change-", []byte(generateFyneModelBuilt(t, inputChangeSrc)), inputChangeDriver)
}
