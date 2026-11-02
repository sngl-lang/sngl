package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// keyNames are the keys a window's `@key` names that type no character, by
// the name fyne gives each and the name a browser does. The keypad's Enter is
// the main keyboard's.
var keyNames = map[fyne.KeyName]string{
	fyne.KeyReturn:    "Enter",
	fyne.KeyEnter:     "Enter",
	fyne.KeyEscape:    "Escape",
	fyne.KeyBackspace: "Backspace",
	fyne.KeyDelete:    "Delete",
	fyne.KeyUp:        "ArrowUp",
	fyne.KeyDown:      "ArrowDown",
	fyne.KeyLeft:      "ArrowLeft",
	fyne.KeyRight:     "ArrowRight",
	fyne.KeyHome:      "Home",
	fyne.KeyEnd:       "End",
	fyne.KeyPageUp:    "PageUp",
	fyne.KeyPageDown:  "PageDown",
}

// fyneKeys is keyNames the other way: the fyne key a name presses.
var fyneKeys = map[string]fyne.KeyName{}

func init() {
	for k, n := range keyNames {
		if _, ok := fyneKeys[n]; !ok || k == fyne.KeyReturn {
			fyneKeys[n] = k
		}
	}
}

// hearKeys has the window's canvas report to OnKey the keys nothing in it
// takes. fyne's driver hands a canvas a key only while nothing on it is
// focused, and hands it every key twice over: a character as a rune, and the
// key as a KeyEvent. The rune is the one reported for a key that types one,
// and the KeyEvent only for the keys keyNames names, so each is reported
// once. Tab never arrives, since the driver moves focus with it.
func (t *Toplevel) hearKeys() {
	c := t.w.Canvas()
	c.SetOnTypedRune(t.typedRune)
	c.SetOnTypedKey(t.typedKey)
}

func (t *Toplevel) typedRune(r rune) {
	t.reportKey(string(r))
}

func (t *Toplevel) typedKey(ev *fyne.KeyEvent) {
	name, ok := keyNames[ev.Name]
	if !ok {
		return
	}
	// An Escape while an overlay is up is the overlay's.
	if ev.Name == fyne.KeyEscape && t.w != nil && t.w.Canvas().Overlays().Top() != nil {
		return
	}
	t.reportKey(name)
}

// reportKey runs OnKey, unless Control, Alt or Super is held: a key pressed
// with one is a shortcut, the system's or fyne's.
func (t *Toplevel) reportKey(name string) {
	if t.OnKey == nil {
		return
	}
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok &&
		d.CurrentKeyModifiers()&(fyne.KeyModifierControl|fyne.KeyModifierAlt|fyne.KeyModifierSuper) != 0 {
		return
	}
	t.OnKey(name)
}

// PressKey is the key name pressed in the window, delivered as fyne's driver
// delivers one: to the widget focused on the window's canvas, which takes it,
// and otherwise to the canvas, whose handlers report it to OnKey. A name
// neither a character nor one keyNames holds is no key fyne has, and is
// delivered nowhere. A window not yet created has nothing focused. This is
// what a test's `key` through the window's `#id` runs.
func (t *Toplevel) PressKey(name string) {
	var c fyne.Canvas
	var focused fyne.Focusable
	if t.w != nil {
		c = t.w.Canvas()
		focused = c.Focused()
	}
	if r := []rune(name); len(r) == 1 {
		switch {
		case focused != nil:
			// The driver hands the focused widget the key before the
			// character, and a button is pressed by the space key's.
			if k := runeKey(r[0]); k != "" {
				focused.TypedKey(&fyne.KeyEvent{Name: k})
			}
			focused.TypedRune(r[0])
		case c != nil && c.OnTypedRune() != nil:
			c.OnTypedRune()(r[0])
		case c == nil:
			t.typedRune(r[0])
		}
		return
	}
	k, ok := fyneKeys[name]
	if !ok {
		return
	}
	ev := &fyne.KeyEvent{Name: k}
	switch {
	case focused != nil:
		focused.TypedKey(ev)
	case c != nil && c.OnTypedKey() != nil:
		c.OnTypedKey()(ev)
	case c == nil:
		t.typedKey(ev)
	}
}

// runeKey is the key fyne names that types r on its own: a letter's, a
// digit's or the space bar's, and "" for another character.
func runeKey(r rune) fyne.KeyName {
	switch {
	case r == ' ':
		return fyne.KeySpace
	case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
		return fyne.KeyName(string(r))
	case r >= 'a' && r <= 'z':
		return fyne.KeyName(string(r - 'a' + 'A'))
	}
	return ""
}
