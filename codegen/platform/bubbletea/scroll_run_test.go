package bubbletea

import "testing"

// scrollRunSrc is a page of rows in a scroll that fills what the window
// leaves it, the next page added as its end comes into view, as the
// Pokédex's grid is.
const scrollRunSrc = `
import seq "sngl:seq"
import ui "sngl:ui"

ui.window {
    var shown = 20
    var pages = 0

    ui.vbox(style={flex=1}) {
        ui.text(value="pages {pages}")
        ui.scroll(style={flex=1}, @reachEnd {
            if shown < 50 {
                shown += 10
                pages += 1
            }
        }) {
            ui.vbox {
                for var i = seq.range(0, shown) {
                    ui.button(text="row {i}", @click {})
                }
            }
        }
    }
}
`

const scrollRunDriver = `package ui

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

// settle draws the view and answers the wakes it signals, as the program's
// loop does.
func settle(m Model) Model {
	for range 10 {
		view(m)
		select {
		case <-m.__wake:
			m = send(m, snglWakeMsg{})
		default:
			return m
		}
	}
	return m
}

// The scroll shows what fits, the wheel over it moves it, and the end
// coming into view adds a page, once at each size.
func TestAScrollCutsWheelsAndLoadsMore(t *testing.T) {
	m := settle(send(New(), tea.WindowSizeMsg{Width: 40, Height: 8}))
	v := view(m)
	if !strings.Contains(v, "row 0") || strings.Contains(v, "row 9") {
		t.Fatalf("an 8-line window drew:\n%s", v)
	}
	if m.pages != 0 {
		t.Fatalf("a scroll at its top loaded %d pages", m.pages)
	}
	m = send(m, tea.MouseWheelMsg{X: 3, Y: 3, Button: tea.MouseWheelDown})
	if v := view(m); strings.Contains(v, "row 0") {
		t.Fatalf("a notch of the wheel moved nothing:\n%s", v)
	}
	for range 3 {
		m = settle(send(m, tea.MouseWheelMsg{X: 3, Y: 3, Button: tea.MouseWheelDown}))
	}
	if m.pages != 1 {
		t.Fatalf("the end in view loaded %d pages, want 1", m.pages)
	}
	if v := view(m); !strings.Contains(v, "row 1") {
		t.Fatalf("the page after:\n%s", v)
	}
	// The wheel above the scroll moves nothing.
	before := view(m)
	m = send(m, tea.MouseWheelMsg{X: 3, Y: 0, Button: tea.MouseWheelUp})
	if view(m) != before {
		t.Fatal("the wheel over the text above the scroll moved it")
	}
}

// Tab brings the row it focuses into view, and PgUp pages back.
func TestAScrollFollowsTab(t *testing.T) {
	m := settle(send(New(), tea.WindowSizeMsg{Width: 40, Height: 8}))
	for range 9 {
		m = send(m, tea.KeyPressMsg{Code: tea.KeyTab})
	}
	if v := view(m); !strings.Contains(v, "> row 9") {
		t.Fatalf("Tab to row 9 drew:\n%s", v)
	}
	m = send(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if v := view(m); !strings.Contains(v, "row 0") {
		t.Fatalf("PgUp drew:\n%s", v)
	}
}
`

func TestScrollsInTheEmittedProgram(t *testing.T) {
	runEmittedProgram(t, scrollRunSrc, scrollRunDriver)
}
