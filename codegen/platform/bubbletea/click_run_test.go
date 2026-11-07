package bubbletea

import "testing"

// clickRunSrc holds what a click presses: a loop of keys Tab passes over, a
// button Tab reaches, a checkbox, and a text field a click only focuses.
const clickRunSrc = `
import ui "sngl:ui"

ui.window {
    var keys = ["seven", "eight"]
    var pressed = ""
    var checked = false
    var name = ""

    ui.vbox {
        ui.hbox {
            for var k = keys {
                ui.button(text=k, focusable=false, @click { pressed = k })
            }
        }
        ui.button(text="reset", @click { pressed = "reset" })
        ui.checkbox(label="sure", :checked=checked)
        ui.input(placeholder="who", :value=name)
        ui.text(value="pressed {pressed}")
    }
}
`

const clickRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

// at is the cell a click on the first line holding label lands on.
func at(t *testing.T, m Model, label string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if x := strings.Index(line, label); x >= 0 {
			return ansi.StringWidth(line[:x]), y
		}
	}
	t.Fatalf("no %q in the view:\n%s", label, ansi.Strip(m.View().Content))
	return 0, 0
}

func click(t *testing.T, m Model, label string) Model {
	t.Helper()
	x, y := at(t, m, label)
	return send(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// A click presses the block under it, the copy of a loop it is on included,
// and a view that takes clicks asks the terminal for them.
func TestAClickPressesWhatIsUnderIt(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 60, Height: 20})
	if v := m.View(); v.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode %v, want cell motion", v.MouseMode)
	}
	if strings.Contains(m.View().Content, "\x1b[0z") {
		t.Fatal("a mark reached the terminal")
	}
	for _, k := range []string{"eight", "seven", "reset"} {
		m = click(t, m, k)
		if m.pressed != k {
			t.Fatalf("clicked %q, pressed %q", k, m.pressed)
		}
	}
	m = click(t, m, "sure")
	if !m.checked {
		t.Fatal("a click on the checkbox left it unchecked")
	}
	m = click(t, m, "pressed")
	if m.pressed != "reset" {
		t.Fatalf("a click on text pressed %q", m.pressed)
	}
}

// A click on a stop Tab passes over gives focus back; one on a stop Tab
// reaches keeps it, and a text field takes the keys after one.
func TestAClickMovesFocusWhereTabWould(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 60, Height: 20})
	if m.__focusID != 1 {
		t.Fatalf("focus starts on stop %d, want the reset button past the keys", m.__focusID)
	}
	m = click(t, m, "eight")
	if m.__focusID != 1 {
		t.Fatalf("a key Tab passes over kept focus: stop %d", m.__focusID)
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pressed != "reset" {
		t.Fatalf("enter after the click pressed %q, want reset", m.pressed)
	}
	m = click(t, m, "who")
	for _, r := range "ada" {
		m = send(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.name != "ada" {
		t.Fatalf("typed after a click on the field: %q", m.name)
	}
}

// Tab passes over every copy of a loop of keys Tab passes over, and the
// focused mark is never drawn on one.
func TestTabPassesOverKeysNotFocusable(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 60, Height: 20})
	for range 6 {
		m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.__focusID == 0 {
			t.Fatal("Tab reached a key written focusable=false")
		}
		if strings.Contains(ansi.Strip(m.View().Content), "> seven") {
			t.Fatal("a key Tab passes over was drawn focused")
		}
	}
}
`

func TestClicksPressTheStopUnderThem(t *testing.T) {
	runEmittedProgram(t, clickRunSrc, clickRunDriver)
}

// cardRunSrc is a loop of clickable cards, each a picture's place and a
// name, as the Pokédex's grid is.
const cardRunSrc = `
import ui "sngl:ui"

ui.window {
    var names = ["bulbasaur", "ivysaur"]
    var opened = ""

    ui.vbox {
        ui.grid(columns=2) {
            for var n = names {
                ui.card(clickable=true, @click { opened = n }) {
                    ui.vbox {
                        ui.text(value="#00{n.length()}")
                        ui.text(value=n)
                    }
                }
            }
        }
        ui.text(value="opened {opened}")
    }
}
`

const cardRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func click(t *testing.T, m Model, label string) Model {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if x := strings.Index(line, label); x >= 0 {
			return send(m, tea.MouseClickMsg{X: ansi.StringWidth(line[:x]), Y: y, Button: tea.MouseLeft})
		}
	}
	t.Fatalf("no %q in the view:\n%s", label, ansi.Strip(m.View().Content))
	return m
}

// A click anywhere on a clickable card opens it, not only on the › before
// its face, and on the card it lands on.
func TestAClickOnACardsFaceOpensIt(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 60, Height: 20})
	m = click(t, m, "ivysaur")
	if m.opened != "ivysaur" {
		t.Fatalf("clicked ivysaur's name, opened %q", m.opened)
	}
	m = click(t, m, "#009")
	if m.opened != "bulbasaur" {
		t.Fatalf("clicked bulbasaur's number, opened %q", m.opened)
	}
}
`

func TestACardsFaceTakesItsClick(t *testing.T) {
	runEmittedProgram(t, cardRunSrc, cardRunDriver)
}

// selectRunSrc is a select in each copy of a loop, as the Pokédex's moves
// tab has, with a button below that its popover covers.
const selectRunSrc = `
import ui "sngl:ui"

ui.window {
    var tabs = ["moves"]
    var method = "Level"
    var changed = 0
    var pressed = 0

    ui.vbox {
        for var tab = tabs {
            ui.select(value=method, @change(v) { method = v; changed += 1 }, options=[
                {value="Level", label="Level"},
                {value="Machine", label="Machine"},
                {value="Egg", label="Egg"},
            ])
        }
        ui.button(text="under", @click { pressed += 1 })
        ui.text(value="method {method} changed {changed} pressed {pressed}")
    }
}
`

const selectRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func view(m Model) string { return ansi.Strip(m.View().Content) }

func at(t *testing.T, m Model, label string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(view(m), "\n") {
		if x := strings.Index(line, label); x >= 0 {
			return ansi.StringWidth(line[:x]), y
		}
	}
	t.Fatalf("no %q in the view:\n%s", label, view(m))
	return 0, 0
}

func click(t *testing.T, m Model, label string) Model {
	t.Helper()
	x, y := at(t, m, label)
	return send(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// A closed select is a button showing its choice; a click on it opens the
// choices over what is below, and a click on one chooses it.
func TestASelectsPopoverChoosesByClick(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 60, Height: 20})
	if v := view(m); !strings.Contains(v, "Level ▾") || strings.Contains(v, "Machine") {
		t.Fatalf("a closed select drew:\n%s", v)
	}
	m = click(t, m, "Level ▾")
	if v := view(m); !strings.Contains(v, "Machine") || strings.Contains(v, "under") {
		t.Fatalf("an open select drew:\n%s", v)
	}
	m = click(t, m, "Egg")
	if m.method != "Egg" || m.changed != 1 || m.pressed != 0 {
		t.Fatalf("a click on Egg: method %q, changed %d, pressed %d", m.method, m.changed, m.pressed)
	}
	if v := view(m); strings.Contains(v, "Machine") {
		t.Fatalf("the popover stayed after a choice:\n%s", v)
	}
}

// Enter opens it and moving the cursor chooses nothing; Esc closes it
// leaving the value, and a click outside closes it without pressing what is
// there.
func TestASelectsPopoverClosesWithoutChoosing(t *testing.T) {
	m := send(New(), tea.WindowSizeMsg{Width: 60, Height: 20})
	m = click(t, m, "Level ▾")
	m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = send(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.method != "Level" || m.changed != 0 || strings.Contains(view(m), "Machine") {
		t.Fatalf("Esc: method %q, changed %d, view:\n%s", m.method, m.changed, view(m))
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(view(m), "Machine") {
		t.Fatalf("Enter on the focused select left it closed:\n%s", view(m))
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = send(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.method != "Machine" || m.changed != 1 {
		t.Fatalf("Down, Enter: method %q, changed %d", m.method, m.changed)
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = click(t, m, "pressed")
	if m.pressed != 0 || strings.Contains(view(m), "Egg") {
		t.Fatalf("a click outside: pressed %d, view:\n%s", m.pressed, view(m))
	}
}
`

func TestASelectOpensAPopover(t *testing.T) {
	runEmittedProgram(t, selectRunSrc, selectRunDriver)
}
