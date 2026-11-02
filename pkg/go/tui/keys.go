package tui

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// keyNames are the keys a window's `@key` names that type nothing, by the
// name a browser gives each.
var keyNames = map[rune]string{
	tea.KeyEnter:     "Enter",
	tea.KeyEsc:       "Escape",
	tea.KeyBackspace: "Backspace",
	tea.KeyDelete:    "Delete",
	tea.KeyUp:        "ArrowUp",
	tea.KeyDown:      "ArrowDown",
	tea.KeyLeft:      "ArrowLeft",
	tea.KeyRight:     "ArrowRight",
	tea.KeyHome:      "Home",
	tea.KeyEnd:       "End",
	tea.KeyPgUp:      "PageUp",
	tea.KeyPgDown:    "PageDown",
}

// KeyName is the name a window's `@key` hears k by: the text it types, or
// the browser's name of a key that types none. It is "" for a key pressed
// with Control, Alt or Meta held, which is a shortcut and no window's, and
// for one the window is not told of.
func KeyName(k tea.KeyPressMsg) string {
	if k.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModSuper|tea.ModHyper) != 0 {
		return ""
	}
	if name, ok := keyNames[k.Code]; ok {
		return name
	}
	if k.Text != "" {
		return k.Text
	}
	return ""
}

// KeyPress is the press of the key named name, as KeyName names it: what a
// test pressing a key sends.
func KeyPress(name string) tea.KeyPressMsg {
	for code, n := range keyNames {
		if n == name {
			return tea.KeyPressMsg{Code: code}
		}
	}
	r, _ := utf8.DecodeRuneInString(name)
	if name == " " {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: r, Text: name}
}
