package tui

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// keyNames are the keys a window's `@key` names that type nothing, by the
// name a browser gives each.
var keyNames = map[rune]string{
	tea.KeyEnter:     "Enter",
	tea.KeyTab:       "Tab",
	tea.KeyEsc:       "Escape",
	tea.KeyBackspace: "Backspace",
	tea.KeyDelete:    "Delete",
	tea.KeyInsert:    "Insert",
	tea.KeyUp:        "ArrowUp",
	tea.KeyDown:      "ArrowDown",
	tea.KeyLeft:      "ArrowLeft",
	tea.KeyRight:     "ArrowRight",
	tea.KeyHome:      "Home",
	tea.KeyEnd:       "End",
	tea.KeyPgUp:      "PageUp",
	tea.KeyPgDown:    "PageDown",
}

// keyCodes is keyNames turned around.
var keyCodes = map[string]rune{}

func init() {
	for i := range 12 {
		keyNames[tea.KeyF1+rune(i)] = fmt.Sprintf("F%d", i+1)
	}
	for code, name := range keyNames {
		keyCodes[name] = code
	}
}

// KeyName is the name a window's `@key` hears k by: the browser's name of a
// key that types nothing, else the character the key types -- its own
// character where Control, Alt or Meta held types none. It is "" for ctrl+c,
// which closes the window and is no key of the window's, and for a key with
// no name.
func KeyName(k tea.KeyPressMsg) string {
	if k.Code == 'c' && k.Mod == tea.ModCtrl {
		return ""
	}
	if name, ok := keyNames[k.Code]; ok {
		return name
	}
	if k.Text != "" {
		return k.Text
	}
	if k.Mod&tea.ModShift != 0 && k.ShiftedCode != 0 {
		return string(k.ShiftedCode)
	}
	if unicode.IsPrint(k.Code) {
		return string(k.Code)
	}
	return ""
}

// KeyPress is the press of the key named name, as KeyName names it, with the
// modifiers held that are true: what a test pressing a key sends. A key held
// with Control, Alt or Meta types nothing.
func KeyPress(name string, ctrl, alt, shift, meta bool) tea.KeyPressMsg {
	var k tea.KeyPressMsg
	if code, ok := keyCodes[name]; ok {
		k.Code = code
	} else if name == " " {
		k = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	} else {
		r, _ := utf8.DecodeRuneInString(name)
		k = tea.KeyPressMsg{Code: unicode.ToLower(r), Text: name}
		if unicode.IsUpper(r) {
			k.ShiftedCode = r
		}
	}
	if ctrl {
		k.Mod |= tea.ModCtrl
	}
	if alt {
		k.Mod |= tea.ModAlt
	}
	if shift {
		k.Mod |= tea.ModShift
	}
	if meta {
		k.Mod |= tea.ModMeta
	}
	if ctrl || alt || meta {
		k.Text = ""
	}
	return k
}
