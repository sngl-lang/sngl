package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func key(c rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: c} }

func TestSelectHoldsItsValueUntilAChoiceIsMade(t *testing.T) {
	f := NewFloats()
	s := NewSelect(40, 10)
	s.SetValue("Banana")
	if got := s.Value(); got != "Banana" {
		t.Fatalf("a value no choice carries yet read %q", got)
	}

	// The choices arrive after the value, and the cursor finds it.
	choices := []string{"Apple", "Banana", "Cherry"}
	s, _ = s.Update(tea.WindowSizeMsg{Width: 40, Height: 10}, choices, f)
	if got := s.Value(); got != "Banana" {
		t.Fatalf("a message that moves nothing chose %q", got)
	}

	// Closed, Down chooses the next.
	s, _ = s.Update(key(tea.KeyDown), choices, f)
	if got := s.Value(); got != "Cherry" {
		t.Fatalf("down from Banana chose %q", got)
	}

	// A value no choice carries stays.
	s.SetValue("Durian")
	s, _ = s.Update(tea.WindowSizeMsg{Width: 40, Height: 10}, choices, f)
	if got := s.Value(); got != "Durian" {
		t.Fatalf("a message that moves nothing chose %q over a value no choice carries", got)
	}
}

// Enter opens the popover; the cursor moves without choosing, and Enter
// chooses the row it is on and closes it.
func TestSelectChoosesFromItsPopover(t *testing.T) {
	f := NewFloats()
	s := NewSelect(40, 10)
	choices := []string{"Apple", "Banana", "Cherry"}
	s.SetValue("Apple")
	s, _ = s.Update(key(tea.KeyEnter), choices, f)
	if !s.Open() || !f.Open() {
		t.Fatal("Enter left the select closed")
	}
	s, _ = s.Update(key(tea.KeyDown), choices, f)
	s, _ = s.Update(key(tea.KeyDown), choices, f)
	if got := s.Value(); got != "Apple" {
		t.Fatalf("moving the cursor in the popover chose %q", got)
	}
	s, _ = s.Update(key(tea.KeyEnter), choices, f)
	if got := s.Value(); got != "Cherry" || s.Open() {
		t.Fatalf("Enter on Cherry chose %q, open %v", got, s.Open())
	}

	// Esc closes it leaving the value.
	s, _ = s.Update(key(tea.KeyEnter), choices, f)
	s, _ = s.Update(key(tea.KeyUp), choices, f)
	s, _ = s.Update(key(tea.KeyEsc), choices, f)
	if got := s.Value(); got != "Cherry" || s.Open() {
		t.Fatalf("Esc chose %q, open %v", got, s.Open())
	}
}

// The view draws a closed select as a button and an open one's choices below
// it, over what is there; a click on a row chooses it, and Close shuts it.
func TestSelectPopoverFloatsBelowItsButton(t *testing.T) {
	f := NewFloats()
	s := NewSelect(40, 10)
	choices := []string{"Level", "Machine", "Egg"}
	s.SetValue("Level")
	closed := ansi.Strip(f.Draw(f.Scan(s.View(choices, f)+"\nbelow", 0, 0), 40, 12))
	if !strings.Contains(closed, "Level ▾") || strings.Contains(closed, "Machine") {
		t.Fatalf("a closed select drew:\n%s", closed)
	}
	s, _ = s.Update(key(tea.KeyEnter), choices, f)
	view := ansi.Strip(f.Draw(f.Scan(s.View(choices, f)+"\nbelow\nbelow\nbelow\nbelow", 0, 0), 40, 12))
	lines := strings.Split(view, "\n")
	// The button is three lines, so the popover's border is the fourth and
	// its rows start at the fifth.
	if !strings.Contains(lines[4], "Level") || !strings.Contains(lines[5], "Machine") || strings.Contains(view, "below\nbelow\nbelow\nbelow") {
		t.Fatalf("an open select drew:\n%s", view)
	}
	if !f.At(2, 5) {
		t.Fatal("a click on the popover missed it")
	}
	s, _ = s.Update(tea.MouseClickMsg{X: 2, Y: 6, Button: tea.MouseLeft}, choices, f)
	if got := s.Value(); got != "Egg" || s.Open() {
		t.Fatalf("a click on Egg chose %q, open %v", got, s.Open())
	}

	s, _ = s.Update(key(tea.KeyEnter), choices, f)
	f.Close()
	if s.Open() || f.Open() {
		t.Fatal("Close left the select open")
	}
}
