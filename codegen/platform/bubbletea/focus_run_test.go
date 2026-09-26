package bubbletea

import "testing"

// focusLoopRunSrc holds a loop slot whose iterations each render several
// focusable nodes: two buttons per row, a nested loop of buttons between them
// under an `if`, and a stateful component spliced once per row with a button
// of its own beside the one its caller hands it.
const focusLoopRunSrc = `
import . "sngl:ui"

struct Row {
    name string
    cells list<string>
}

component Card(children ...component) node {
    var hits = 0

    vbox {
        button(text="hit {hits}", @click { hits = hits + 1 })
        children
    }
}

window {
    var rows = [Row{name="r1", cells=["a", "b"]}, Row{name="r2", cells=["c"]}]
    var picked = ""
    var open = true

    vbox {
        for var r = rows {
            vbox {
                button(text=r.name, @click { picked = r.name })
                if open {
                    for var c = r.cells {
                        button(text=c, @click { picked = r.name + c })
                    }
                }
                Card {
                    button(text="pick", @click { picked = "card " + r.name })
                }
            }
        }
    }
}
`

const focusLoopRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func press(m Model) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(Model)
}

func tab(m Model) Model {
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	return next.(Model)
}

// Every focusable node the loop renders is one stop, in render order, and
// Enter runs that node's own handler with that iteration's variables.
func TestEveryNodeInTheLoopIsItsOwnStop(t *testing.T) {
	want := []string{"r1", "r1a", "r1b", "hit", "card r1", "r2", "r2c", "hit", "card r2"}
	m := New()
	for i, w := range want {
		m.picked = ""
		m = press(m)
		got := m.picked
		if w == "hit" {
			if got != "" {
				t.Fatalf("stop %d ran %q, want the card's own button", i, got)
			}
		} else if got != w {
			t.Fatalf("stop %d ran %q, want %q", i, got, w)
		}
		m = tab(m)
	}
	m.picked = ""
	m = press(m)
	if m.picked != "r1" {
		t.Fatalf("focus did not wrap to the first stop: %q", m.picked)
	}
}

// A card's own button counts that card's hits and no other's.
func TestACardButtonWritesItsOwnCell(t *testing.T) {
	m := New()
	for range 3 {
		m = tab(m)
	}
	m = press(m)
	for range 4 {
		m = tab(m)
	}
	m = press(m)
	m = press(m)
	if m.hits__inst0["0"] != 1 || m.hits__inst0["1"] != 2 {
		t.Fatalf("hits per card are %v, want 1 and 2", m.hits__inst0)
	}
}

// A branch that renders nothing takes no stop.
func TestAClosedBranchTakesNoStop(t *testing.T) {
	m := New()
	m.open = false
	want := []string{"r1", "hit", "card r1", "r2", "hit", "card r2", "r1"}
	for i, w := range want {
		m.picked = ""
		m = press(m)
		if w != "hit" && m.picked != w {
			t.Fatalf("stop %d ran %q, want %q", i, m.picked, w)
		}
		m = tab(m)
	}
}
`

func TestFocusReachesEveryNodeALoopRenders(t *testing.T) {
	runEmittedProgram(t, focusLoopRunSrc, focusLoopRunDriver)
}
