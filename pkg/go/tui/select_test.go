package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSelectHoldsItsValueUntilTheCursorMoves(t *testing.T) {
	s := NewSelect(40, 10)
	s.SetValue("Banana")
	if got := s.Value(); got != "Banana" {
		t.Fatalf("a value no choice carries yet read %q", got)
	}

	// The choices arrive after the value, and the cursor finds it.
	choices := []string{"Apple", "Banana", "Cherry"}
	s, _ = s.Update(tea.WindowSizeMsg{Width: 40, Height: 10}, choices)
	if got := s.Value(); got != "Banana" {
		t.Fatalf("a message that moves nothing chose %q", got)
	}

	s, _ = s.Update(tea.KeyPressMsg{Code: tea.KeyDown}, choices)
	if got := s.Value(); got != "Cherry" {
		t.Fatalf("down from Banana chose %q", got)
	}

	// A value no choice carries stays, where the cursor is on another row.
	s.SetValue("Durian")
	s, _ = s.Update(tea.WindowSizeMsg{Width: 40, Height: 10}, choices)
	if got := s.Value(); got != "Durian" {
		t.Fatalf("a message that moves nothing chose %q over a value no choice carries", got)
	}
}

func TestSelectEnterChoosesTheRowTheCursorIsOn(t *testing.T) {
	s := NewSelect(40, 10)
	choices := []string{"Apple"}
	s, _ = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, choices)
	if got := s.Value(); got != "Apple" {
		t.Fatalf("Enter on the one choice chose %q", got)
	}
}
