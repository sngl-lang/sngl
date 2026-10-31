package bubbletea

import "testing"

const bindEffectRunSrc = `
import ui "sngl:ui"

ui.window {
    var name = ""
    var renames = 0

    effect(on=name, @update { renames += 1 })
    ui.input(:value=name)
}
`

const bindEffectRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k tea.Msg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

// A key the input takes writes the bound var back and runs the @update keyed
// on it; one that moves nothing runs none.
func TestTypingRunsTheUpdate(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.name != "a" || m.renames != 1 {
		t.Fatalf("after a: name %q, renames %d; want \"a\", 1", m.name, m.renames)
	}
	m = key(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.renames != 1 {
		t.Fatalf("a key moving nothing ran the update: renames %d", m.renames)
	}
}
`

func TestTypingIntoABoundInputRunsItsEffects(t *testing.T) {
	runEmittedProgram(t, bindEffectRunSrc, bindEffectRunDriver)
}
