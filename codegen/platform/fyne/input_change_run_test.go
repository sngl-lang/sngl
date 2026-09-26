package fyne

import "testing"

const inputChangeSrc = `
import . "sngl:ui"

var last = ""

window {
    vbox {
        input #box(@change(e) { last = "got " + e.value })
        select #pick(options=["x", "y"], @change(e) { last = "sel " + e.value })
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
	m.box.OnSubmitted("hello")
	if m.last != "got hello" {
		t.Fatalf("after submitting the entry last = %q, want %q", m.last, "got hello")
	}
	m.pick.SetSelected("y")
	if m.last != "sel y" {
		t.Fatalf("after selecting last = %q, want %q", m.last, "sel y")
	}
}
`

// An input's @change fires on commit with the committed text, the payload the
// interpreter gives.
func TestAnInputChangeRuns(t *testing.T) {
	runEmitted(t, "fyne-input-change-", []byte(generateFyneModelBuilt(t, inputChangeSrc)), inputChangeDriver)
}
