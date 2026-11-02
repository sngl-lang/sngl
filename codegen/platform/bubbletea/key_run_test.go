package bubbletea

import "testing"

// keyRunSrc holds a window that hears keys, a button that takes the Enter
// pressed on it, and a field that takes what is typed. Its handler declares a
// local named as the override's own parameter is, which the splice renames.
const keyRunSrc = `
import ui "sngl:ui"

var heard = ""
var pressed = 0
var text = ""
var typing = false

ui.window(@key(name) {
    var k = "[" + name + "]"
    heard = heard + k
}) {
    ui.vbox {
        ui.button(text="go", @click { pressed += 1 })
        if typing {
            ui.input(:value=text)
        }
        ui.text(value=heard)
    }
}
`

const keyRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

// The window hears what nothing in it takes: what is typed, the named keys,
// and not the Enter that presses the focused button, the Tab that moves
// focus, or a key with Control held.
func TestTheWindowHearsKeysNothingTakes(t *testing.T) {
	m := New()
	for _, k := range []tea.KeyPressMsg{
		{Code: '7', Text: "7"},
		{Code: '+', Text: "+"},
		{Code: tea.KeyBackspace},
		{Code: tea.KeyEnter},
		{Code: tea.KeyTab},
		{Code: 's', Mod: tea.ModCtrl},
		{Code: tea.KeyEsc},
	} {
		m = send(m, k)
	}
	if want := "[7][+][Backspace][Escape]"; m.heard != want {
		t.Fatalf("heard %q, want %q", m.heard, want)
	}
	if m.pressed != 1 {
		t.Fatalf("enter pressed the button %d times, want 1", m.pressed)
	}
}

// What is typed into a focused field is the field's.
func TestAFocusedFieldTakesTheKeys(t *testing.T) {
	m := New()
	m.typing = true
	m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
	for _, r := range "hi" {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.text != "hi" || m.heard != "" {
		t.Fatalf("typed into the field: text %q, the window heard %q", m.text, m.heard)
	}
}
`

func TestAWindowHearsItsKeys(t *testing.T) {
	runEmittedProgram(t, keyRunSrc, keyRunDriver)
}
