package fynelayout

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// KeyPress is a key pressed in a window, as its `@key` hears it: a
// `ui.KeyPress`, whose fields these are, in its order.
type KeyPress struct {
	Key     string
	Ctrl    bool
	Alt     bool
	Shift   bool
	Meta    bool
	Editing bool
}

// keyPresses are the Go types a `ui.KeyPress` is: the generated program's own
// struct, which this package cannot name, and KeyPress, which it converts to
// and from because the two have one shape.
type keyPresses interface {
	~struct {
		Key     string
		Ctrl    bool
		Alt     bool
		Shift   bool
		Meta    bool
		Editing bool
	}
}

// HearKeys makes f the window's `@key`: it hears each key pressed in the
// window before anything in it does, and a key it calls stop for goes no
// further. A nil f hears nothing.
func HearKeys[K keyPresses](t *Toplevel, f func(e K, stop func())) {
	if f == nil {
		t.OnKey = nil
		return
	}
	t.OnKey = func(e KeyPress) bool {
		stopped := false
		f(K(e), func() { stopped = true })
		return stopped
	}
}

// PressKey is a test's key e pressed in the window: OnKey hears it as the
// test wrote it, and a key it stops is the test's stop called and nothing
// more. One it leaves is delivered as fyne's driver delivers it (TypeKey),
// unheard, since the window has heard it already.
func PressKey[K keyPresses](t *Toplevel, e K, stop func()) {
	if t.OnKey != nil && t.OnKey(KeyPress(e)) {
		if stop != nil {
			stop()
		}
		return
	}
	t.muted = true
	defer func() { t.muted = false }()
	t.TypeKey(KeyPress(e))
}

// keyNames are the keys a window's `@key` names that type no character, by
// the name fyne gives each and the name a browser does. The keypad's Enter is
// the main keyboard's.
var keyNames = map[fyne.KeyName]string{
	fyne.KeyReturn:    "Enter",
	fyne.KeyEnter:     "Enter",
	fyne.KeyTab:       "Tab",
	fyne.KeyEscape:    "Escape",
	fyne.KeyBackspace: "Backspace",
	fyne.KeyDelete:    "Delete",
	fyne.KeyInsert:    "Insert",
	fyne.KeyUp:        "ArrowUp",
	fyne.KeyDown:      "ArrowDown",
	fyne.KeyLeft:      "ArrowLeft",
	fyne.KeyRight:     "ArrowRight",
	fyne.KeyHome:      "Home",
	fyne.KeyEnd:       "End",
	fyne.KeyPageUp:    "PageUp",
	fyne.KeyPageDown:  "PageDown",
	fyne.KeyF1:        "F1",
	fyne.KeyF2:        "F2",
	fyne.KeyF3:        "F3",
	fyne.KeyF4:        "F4",
	fyne.KeyF5:        "F5",
	fyne.KeyF6:        "F6",
	fyne.KeyF7:        "F7",
	fyne.KeyF8:        "F8",
	fyne.KeyF9:        "F9",
	fyne.KeyF10:       "F10",
	fyne.KeyF11:       "F11",
	fyne.KeyF12:       "F12",
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

// Where a key is heard. fyne's driver hands a press to the focused widget,
// or to the canvas when nothing is focused, in steps: the key going down
// (KeyDown, the canvas's OnKeyDown); then Tab, which moves focus unless the
// focused widget accepts it; then a key held with Control, Alt or Super, which
// is a shortcut (TypedShortcut) and goes no further; then the key
// (TypedKey); and last, from a separate callback, the character it types
// (TypedRune). A key is heard at the first step that names it as `@key` does:
//
//   - a character at its rune, which carries the shift and the layout;
//   - the space bar at its key, which a button is pressed by before the rune;
//   - a key keyNames holds at its key;
//   - Tab at the focused widget's AcceptsTab, which the driver asks before it
//     moves focus, and with nothing focused at the canvas's key down;
//   - a key held with Control, Alt or Super at its shortcut, and with nothing
//     focused at the key down, since the canvas's shortcuts are fyne's.
//
// A key heard ahead of a later step -- the space bar's rune, a Tab the widget
// accepts, a shortcut's rune -- is recorded (heard), and that step follows
// the verdict without the window hearing the key again.

// hear runs OnKey for key, with the modifiers held, and reports whether it
// stopped the key. editing is whether a field has focus.
func (t *Toplevel) hear(key string, editing bool) bool {
	m := modifiers()
	return t.hearPress(KeyPress{
		Key:     key,
		Ctrl:    m&fyne.KeyModifierControl != 0,
		Alt:     m&fyne.KeyModifierAlt != 0,
		Shift:   m&fyne.KeyModifierShift != 0,
		Meta:    m&fyne.KeyModifierSuper != 0,
		Editing: editing,
	})
}

func (t *Toplevel) hearPress(e KeyPress) bool {
	t.heard = ""
	tabHeld = false
	if t.muted || t.OnKey == nil {
		return false
	}
	return t.OnKey(e)
}

// ahead records a key heard before the step that delivers it.
func (t *Toplevel) ahead(key string, stopped bool) {
	t.heard, t.heardStopped = key, stopped
}

// pending is the key heard ahead and its verdict, which it clears.
func (t *Toplevel) pending() (string, bool) {
	k, s := t.heard, t.heardStopped
	t.heard = ""
	return k, s
}

func (t *Toplevel) typedKey(ev *fyne.KeyEvent, editing bool) bool {
	if k, stopped := t.pending(); k == "Tab" && ev.Name == fyne.KeyTab {
		return stopped
	}
	if ev.Name == fyne.KeySpace {
		stopped := t.hear(" ", editing)
		t.ahead(" ", stopped)
		return stopped
	}
	name, ok := keyNames[ev.Name]
	if !ok {
		// A character, heard at its rune.
		return false
	}
	return t.hear(name, editing)
}

func (t *Toplevel) typedRune(r rune, editing bool) bool {
	if k, stopped := t.pending(); k != "" && strings.EqualFold(k, string(r)) {
		return stopped
	}
	if modifiers()&(fyne.KeyModifierControl|fyne.KeyModifierAlt|fyne.KeyModifierSuper) != 0 {
		// Heard as its shortcut.
		return false
	}
	return t.hear(string(r), editing)
}

// typedShortcut hears a shortcut by the key it is pressed with and the
// modifiers it names.
func (t *Toplevel) typedShortcut(s fyne.Shortcut, editing bool) bool {
	ks, ok := s.(fyne.KeyboardShortcut)
	if !ok {
		return false
	}
	key := shortcutKey(ks.Key(), ks.Mod())
	if key == "" {
		return false
	}
	m := ks.Mod()
	stopped := t.hearPress(KeyPress{
		Key:     key,
		Ctrl:    m&fyne.KeyModifierControl != 0,
		Alt:     m&fyne.KeyModifierAlt != 0,
		Shift:   m&fyne.KeyModifierShift != 0,
		Meta:    m&fyne.KeyModifierSuper != 0,
		Editing: editing,
	})
	t.ahead(key, stopped)
	return stopped
}

// keyDown is a key going down with nothing focused: Tab, which the driver
// moves focus with next, and a key held with Control, Alt or Super, which is
// the canvas's shortcut. A stopped Tab is held (tabHeld) until it is
// released, so the focus it would move has nowhere to go.
func (t *Toplevel) keyDown(ev *fyne.KeyEvent) {
	if isModifierKey(ev.Name) {
		return
	}
	m := modifiers()
	switch {
	case m&(fyne.KeyModifierControl|fyne.KeyModifierAlt|fyne.KeyModifierSuper) != 0:
		if key := shortcutKey(ev.Name, m); key != "" {
			t.ahead(key, t.hear(key, false))
		}
	case ev.Name == fyne.KeyTab:
		if t.hear("Tab", false) {
			tabHeld = true
		}
	}
}

func (t *Toplevel) keyUp(ev *fyne.KeyEvent) {
	if ev.Name == fyne.KeyTab {
		tabHeld = false
	}
}

// tabHeld is a stopped Tab held down with nothing focused, which every widget
// here refuses focus through (focus.go). One keyboard presses one Tab, so
// there is one for the program.
var tabHeld bool

// shortcutKey is the key a shortcut is pressed with, as `@key` names it: the
// key's own character, a capital with Shift held, or the name keyNames gives
// it; "" for a modifier.
func shortcutKey(k fyne.KeyName, m fyne.KeyModifier) string {
	if n, ok := keyNames[k]; ok {
		return n
	}
	if k == fyne.KeySpace {
		return " "
	}
	if isModifierKey(k) || k == fyne.KeyUnknown {
		return ""
	}
	s := string(k)
	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' && m&fyne.KeyModifierShift == 0 {
		s = strings.ToLower(s)
	}
	return s
}

func isModifierKey(k fyne.KeyName) bool {
	switch k {
	case desktop.KeyShiftLeft, desktop.KeyShiftRight, desktop.KeyControlLeft, desktop.KeyControlRight,
		desktop.KeyAltLeft, desktop.KeyAltRight, desktop.KeySuperLeft, desktop.KeySuperRight,
		desktop.KeyCapsLock, desktop.KeyMenu, desktop.KeyPrintScreen:
		return true
	}
	return false
}

// typing is the modifiers a key TypeKey delivers is pressed with, which no
// driver holds.
var typing *fyne.KeyModifier

// modifiers is the modifier keys held: the desktop driver's, or the ones the
// key TypeKey is delivering is pressed with.
func modifiers() fyne.KeyModifier {
	if typing != nil {
		return *typing
	}
	if a := fyne.CurrentApp(); a != nil {
		if d, ok := a.Driver().(desktop.Driver); ok {
			return d.CurrentKeyModifiers()
		}
	}
	return 0
}

// windowOf is the Toplevel whose window shows o, or nil.
func windowOf(o fyne.CanvasObject) *Toplevel {
	a := fyne.CurrentApp()
	if a == nil {
		return nil
	}
	c := a.Driver().CanvasForObject(o)
	if c == nil {
		return nil
	}
	for _, t := range attached {
		if t.w != nil && t.w.Canvas() == c {
			return t
		}
	}
	return nil
}

// The steps a focused widget here is handed a key in, each reporting whether
// the window stopped it.

func typedKey(o fyne.CanvasObject, ev *fyne.KeyEvent, editing bool) bool {
	t := windowOf(o)
	return t != nil && t.typedKey(ev, editing)
}

func typedRune(o fyne.CanvasObject, r rune, editing bool) bool {
	t := windowOf(o)
	return t != nil && t.typedRune(r, editing)
}

// typedShortcut hands s on to own, the widget's own shortcuts, unless the
// window stops it; a widget with none hands it to the canvas, as the driver
// does a shortcut no focused widget takes.
func typedShortcut(o fyne.CanvasObject, s fyne.Shortcut, editing bool, own func(fyne.Shortcut)) {
	if t := windowOf(o); t != nil && t.typedShortcut(s, editing) {
		return
	}
	if own != nil {
		own(s)
		return
	}
	if a := fyne.CurrentApp(); a != nil {
		if c, ok := a.Driver().CanvasForObject(o).(fyne.Shortcutable); ok {
			c.TypedShortcut(s)
		}
	}
}

// acceptsTab is the driver asking, before it moves focus, whether the focused
// widget takes Tab itself, which own says: a stopped Tab is taken, and goes
// no further than that widget's TypedKey.
func acceptsTab(o fyne.CanvasObject, editing, own bool) bool {
	t := windowOf(o)
	if t == nil {
		return own
	}
	stopped := t.hear("Tab", editing)
	if stopped || own {
		t.ahead("Tab", stopped)
	}
	return stopped || own
}

// hearKeys has the window's canvas hear the keys pressed while nothing on it
// is focused, which fyne's driver hands the canvas alone.
func (t *Toplevel) hearKeys() {
	c := t.w.Canvas()
	c.SetOnTypedRune(func(r rune) { t.typedRune(r, false) })
	c.SetOnTypedKey(func(ev *fyne.KeyEvent) { t.typedKey(ev, false) })
	if dc, ok := c.(desktop.Canvas); ok {
		dc.SetOnKeyDown(t.keyDown)
		dc.SetOnKeyUp(t.keyUp)
	}
}

// TypeKey is e pressed in the window, delivered as fyne's desktop driver
// delivers a press, step by step, to the widget focused on the window's
// canvas or, with nothing focused, to the canvas's handlers; a window not yet
// created has nothing focused. e's modifiers are the ones held while it is,
// and its Editing is the focus's to say. A name neither a character nor one
// keyNames holds is no key fyne has, and is delivered nowhere.
func (t *Toplevel) TypeKey(e KeyPress) {
	var c fyne.Canvas
	var focused fyne.Focusable
	if t.w != nil {
		c = t.w.Canvas()
		focused = c.Focused()
	}
	r := []rune(e.Key)
	char := len(r) == 1
	var name fyne.KeyName
	switch k, ok := fyneKeys[e.Key]; {
	case ok:
		name = k
	case char:
		name = runeKey(r[0])
	default:
		return
	}
	var m fyne.KeyModifier
	if e.Ctrl {
		m |= fyne.KeyModifierControl
	}
	if e.Alt {
		m |= fyne.KeyModifierAlt
	}
	if e.Shift {
		m |= fyne.KeyModifierShift
	}
	if e.Meta {
		m |= fyne.KeyModifierSuper
	}
	prev := typing
	typing = &m
	defer func() { typing = prev }()

	ev := &fyne.KeyEvent{Name: name}
	if k, ok := focused.(desktop.Keyable); ok {
		k.KeyDown(ev)
		defer k.KeyUp(ev)
	} else if focused == nil {
		t.keyDown(ev)
		defer t.keyUp(ev)
	}
	shortcut := m &^ fyne.KeyModifierShift
	if name == fyne.KeyTab && shortcut == 0 {
		if tb, ok := focused.(fyne.Tabbable); !ok || !tb.AcceptsTab() {
			switch {
			case c == nil:
			case m == fyne.KeyModifierShift:
				c.FocusPrevious()
			default:
				c.FocusNext()
			}
			return
		}
	}
	if shortcut != 0 {
		s := shortcutFor(name, m)
		if sc, ok := focused.(fyne.Shortcutable); ok {
			sc.TypedShortcut(s)
		} else if sc, ok := c.(fyne.Shortcutable); ok {
			sc.TypedShortcut(s)
		}
		return
	}
	if focused != nil {
		focused.TypedKey(ev)
	} else {
		t.typedKey(ev, false)
	}
	if !char {
		return
	}
	if focused != nil {
		focused.TypedRune(r[0])
	} else {
		t.typedRune(r[0], false)
	}
}

// shortcutFor is the shortcut the driver makes of name pressed with m: one of
// the clipboard's, undo's or select-all's with Control alone, and otherwise
// a desktop.CustomShortcut.
func shortcutFor(name fyne.KeyName, m fyne.KeyModifier) fyne.Shortcut {
	if m == fyne.KeyModifierControl {
		switch name {
		case fyne.KeyZ:
			return &fyne.ShortcutUndo{}
		case fyne.KeyY:
			return &fyne.ShortcutRedo{}
		case fyne.KeyV:
			return &fyne.ShortcutPaste{Clipboard: fyne.CurrentApp().Clipboard()}
		case fyne.KeyC:
			return &fyne.ShortcutCopy{Clipboard: fyne.CurrentApp().Clipboard()}
		case fyne.KeyX:
			return &fyne.ShortcutCut{Clipboard: fyne.CurrentApp().Clipboard()}
		case fyne.KeyA:
			return &fyne.ShortcutSelectAll{}
		}
	}
	return &desktop.CustomShortcut{KeyName: name, Modifier: m}
}

// runeKey is the key fyne names that types r: a letter's, by its capital, and
// otherwise the character's own name, as fyne names "7", "=" or the space
// bar.
func runeKey(r rune) fyne.KeyName {
	switch {
	case r == ' ':
		return fyne.KeySpace
	case r >= 'a' && r <= 'z':
		return fyne.KeyName(string(r - 'a' + 'A'))
	}
	return fyne.KeyName(string(r))
}
