package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A key is named as a browser names it, and the press KeyPress builds is
// heard by that name again.
func TestKeyNamesRoundTrip(t *testing.T) {
	for _, name := range []string{"7", "+", "a", "A", " ", "é", "Enter", "Escape", "Backspace", "ArrowUp", "PageDown"} {
		if got := KeyName(KeyPress(name)); got != name {
			t.Errorf("KeyName(KeyPress(%q)) = %q", name, got)
		}
	}
}

// A key pressed with Control, Alt or Meta held is no window's, and Tab is
// focus's.
func TestKeyNamesLeaveShortcutsAndTab(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{
		{Code: 's', Mod: tea.ModCtrl},
		{Code: 'x', Text: "x", Mod: tea.ModAlt},
		{Code: tea.KeyEnter, Mod: tea.ModMeta},
		{Code: tea.KeyTab},
	} {
		if got := KeyName(k); got != "" {
			t.Errorf("KeyName(%v) = %q, want none", k, got)
		}
	}
	if got := KeyName(tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift}); got != "A" {
		t.Errorf("shift+a named %q, want A", got)
	}
}
