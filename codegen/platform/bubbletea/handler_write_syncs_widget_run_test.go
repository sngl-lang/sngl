package bubbletea

import "testing"

const handlerWriteSyncsWidgetSrc = `
import ui "sngl:ui"

ui.window {
    var memo = ""

    ui.vbox {
        ui.input(:value=memo)
        ui.button(text="set", @click { memo = "abc" })
    }
}
`

const handlerWriteSyncsWidgetDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k tea.Msg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

// A key the input gets after the button's write edits what the write left,
// not the text the input showed before it.
func TestTypingAfterAHandlerWriteKeepsIt(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = key(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.memo != "abc" {
		t.Fatalf("after click: memo %q, want \"abc\"", m.memo)
	}
	m = key(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = key(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.memo != "abcx" {
		t.Fatalf("after typing: memo %q, want \"abcx\"", m.memo)
	}
}
`

func TestAHandlerWriteReachesItsBoundWidget(t *testing.T) {
	runEmittedProgram(t, handlerWriteSyncsWidgetSrc, handlerWriteSyncsWidgetDriver)
}
