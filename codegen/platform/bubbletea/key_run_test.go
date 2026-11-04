package bubbletea

import "testing"

// keyRunSrc holds a window that hears keys and stops Control's and the x, a
// button that takes the Enter pressed on it, and a field that takes what is
// typed. Its handler declares a local named as the override's own parameter
// is, which the splice renames.
const keyRunSrc = `
import ui "sngl:ui"

var heard = ""
var pressed = 0
var text = ""
var typing = false

ui.window(@key(e, stop) {
    var k = "[" + e.key + "]"
    if e.ctrl {
        k = "[^" + e.key + "]"
    }
    if e.editing {
        k = "<" + e.key + ">"
    }
    heard = heard + k
    if e.ctrl || e.key == "x" {
        stop()
    }
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

// The window hears every key before anything in it, and a key it leaves goes
// on: the Enter it heard still presses the focused button. ctrl+c is the
// close, and no key the window hears.
func TestTheWindowHearsEveryKeyFirst(t *testing.T) {
	m := New()
	for _, k := range []tea.KeyPressMsg{
		{Code: '7', Text: "7"},
		{Code: tea.KeyBackspace},
		{Code: tea.KeyEnter},
		{Code: tea.KeyTab},
		{Code: 's', Mod: tea.ModCtrl},
		{Code: tea.KeyEsc},
	} {
		m = send(m, k)
	}
	if want := "[7][Backspace][Enter][Tab][^s][Escape]"; m.heard != want {
		t.Fatalf("heard %q, want %q", m.heard, want)
	}
	if m.pressed != 1 {
		t.Fatalf("enter pressed the button %d times, want 1", m.pressed)
	}
}

// A key the handler stops reaches nothing else: the Enter a Control holds
// presses no button.
func TestAStoppedKeyGoesNoFurther(t *testing.T) {
	m := send(New(), tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if m.pressed != 0 {
		t.Fatalf("a stopped enter pressed the button %d times", m.pressed)
	}
}

// While a field holds focus the window hears its keys as editing, and the
// field takes those the window leaves.
func TestAFocusedFieldIsEditing(t *testing.T) {
	m := New()
	m.typing = true
	m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m.heard = ""
	for _, r := range "hix" {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.text != "hi" || m.heard != "<h><i><x>" {
		t.Fatalf("typed into the field: text %q, the window heard %q", m.text, m.heard)
	}
}

// A click pressing a button presses no key the window hears.
func TestAClickIsNoKey(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 40, Height: 10})
	_ = m.View()
	m = send(m, tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if m.pressed != 1 || m.heard != "" {
		t.Fatalf("a click pressed %d times, the window heard %q", m.pressed, m.heard)
	}
}
`

func TestAWindowHearsItsKeys(t *testing.T) {
	runEmittedProgram(t, keyRunSrc, keyRunDriver)
}
