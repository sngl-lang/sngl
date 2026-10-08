package bubbletea

import "testing"

// loopWidgetRunSrc renders a text input per row of a loop, each starting from
// its row's text, and handlers that read the row they were written in.
const loopWidgetRunSrc = `
import . "sngl:ui"

window {
    var items = ["a", "b"]
    var log = ""

    vbox {
        for var it = items {
            input(value=it, @input(v) { log = it + ":" + v }, @change(v) { log = it + "=" + v })
        }
        text(value=log)
    }
}
`

const loopWidgetRunDriver = `package ui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k tea.KeyPressMsg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

func typed(m Model, s string) Model {
	for _, r := range s {
		m = key(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// Each row is a model of its own: what is typed into the second goes into the
// second, which still shows its own text, and the handlers read that row.
func TestEachRowIsAModelOfItsOwn(t *testing.T) {
	m := New()
	m.View()
	m = key(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = typed(m, "x")
	if m.log != "b:bx" {
		t.Fatalf("typing into the second row logged %q, want %q", m.log, "b:bx")
	}
	m = key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.log != "b=bx" {
		t.Fatalf("committing the second row logged %q, want %q", m.log, "b=bx")
	}
	if v := m.View().Content; !strings.Contains(v, "a") || !strings.Contains(v, "bx") {
		t.Fatalf("the rows do not each show their own text:\n%s", v)
	}
	m = key(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = typed(m, "y")
	if m.log != "a:ay" {
		t.Fatalf("typing into the first row logged %q, want %q", m.log, "a:ay")
	}
}
`

func TestALoopRendersAWidgetPerRow(t *testing.T) {
	runEmittedProgram(t, loopWidgetRunSrc, loopWidgetRunDriver)
}
