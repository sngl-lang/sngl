package bubbletea

import "testing"

const inputSubmitRunSrc = `
import ui "sngl:ui"

ui.window {
    var text = ""
    var order = ""

    ui.input(:value=text, @change(v) { order = order + "c" }, @submit { order = order + "s" })
}
`

const inputSubmitRunDriver = `package ui

import (
	"testing"

	"charm.land/bubbletea/v2"
)

func key(m Model, k tea.Msg) Model {
	next, _ := m.Update(k)
	return next.(Model)
}

func enter(m Model) Model { return key(m, tea.KeyPressMsg{Code: tea.KeyEnter}) }

// Enter commits an edit made since the last commit, then submits; pressed
// again unedited it only submits.
func TestEnterCommitsAnEditThenSubmits(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, tea.KeyPressMsg{Code: 'h', Text: "h"})
	m = enter(m)
	if m.text != "h" || m.order != "cs" {
		t.Fatalf("first Enter: text %q, order %q; want \"h\", \"cs\"", m.text, m.order)
	}
	m = enter(m)
	if m.order != "css" {
		t.Fatalf("second Enter: order %q; want \"css\"", m.order)
	}
}

// The program's write, made while the field has focus, is no edit.
func TestEnterAfterAProgramWriteOnlySubmits(t *testing.T) {
	m := key(New(), tea.WindowSizeMsg{Width: 80, Height: 24})
	m = key(m, setTextMsg{value: "abc"})
	m = enter(m)
	if m.text != "abc" || m.order != "s" {
		t.Fatalf("text %q, order %q; want \"abc\", \"s\"", m.text, m.order)
	}
}
`

func TestAnInputCommitsAnEditAndSubmitsOnEnter(t *testing.T) {
	runEmittedProgram(t, inputSubmitRunSrc, inputSubmitRunDriver)
}
