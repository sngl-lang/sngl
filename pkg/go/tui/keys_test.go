package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A key is named as a browser names it, and the press KeyPress builds is
// heard by that name again.
func TestKeyNamesRoundTrip(t *testing.T) {
	for _, name := range []string{"7", "+", "a", "A", " ", "é", "Enter", "Escape", "Backspace", "ArrowUp", "PageDown", "Tab", "F12"} {
		if got := KeyName(KeyPress(name, false, false, false, false)); got != name {
			t.Errorf("KeyName(KeyPress(%q)) = %q", name, got)
		}
	}
}

// A key pressed with Control, Alt or Meta held is named by its own
// character, and ctrl+c, the close, by none.
func TestKeyNamesWithModifiers(t *testing.T) {
	for k, want := range map[tea.KeyPressMsg]string{
		{Code: 's', Mod: tea.ModCtrl}:                                  "s",
		{Code: 's', ShiftedCode: 'S', Mod: tea.ModCtrl | tea.ModShift}: "S",
		{Code: 'x', Text: "x", Mod: tea.ModAlt}:                        "x",
		{Code: tea.KeyEnter, Mod: tea.ModMeta}:                         "Enter",
		{Code: tea.KeyTab, Mod: tea.ModShift}:                          "Tab",
		{Code: tea.KeyF5}:                                              "F5",
		{Code: 'a', Text: "A", Mod: tea.ModShift}:                      "A",
		{Code: 'c', Mod: tea.ModCtrl}:                                  "",
	} {
		if got := KeyName(k); got != want {
			t.Errorf("KeyName(%v) = %q, want %q", k, got, want)
		}
	}
}

// A press a test builds with Control held types nothing, and is heard by the
// key's own character.
func TestKeyPressHoldsModifiers(t *testing.T) {
	k := KeyPress("s", true, false, false, false)
	if k.Text != "" || k.Mod != tea.ModCtrl || KeyName(k) != "s" {
		t.Fatalf("ctrl+s built as %#v, named %q", k, KeyName(k))
	}
}
