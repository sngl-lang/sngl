package fyne

import "testing"

const windowKeySrc = `
import ui "sngl:ui"

var heard = ""
var stopping = false
var presses = 0
var skips = 0

ui.window #main(title="Keys", @key(e, stop) {
    heard = heard + "[" + e.key + (e.ctrl ? "^" : "") + (e.editing ? ":e" : "") + "]"
    if stopping || e.key == "x" || e.ctrl {
        stop()
    }
}) {
    ui.input #field()
    ui.button #skip(text="skip", focusable=false, @click { skips += 1 })
    ui.button #next(text="next", @click { presses += 1 })
}
`

const windowKeyDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"duckfam.us/sngl/pkg/go/fynelayout"
)

func TestTheWindowHearsEveryKeyFirst(t *testing.T) {
	a := test.NewApp()
	m := New()
	m.BuildUI()
	fynelayout.RunWindows(a, true)
	// The test driver's canvas for any object is its last window's, which is
	// the program's: RunWindows makes its anchor first.
	c := fyne.CurrentApp().Driver().CanvasForObject(m.main.Container)
	if c.Content() != m.main.Container {
		t.Fatal("the last window is not the program's")
	}
	press := func(key string) { m.main.TypeKey(fynelayout.KeyPress{Key: key}) }
	expect := func(what, want string) {
		t.Helper()
		if m.heard != want {
			t.Fatalf("%s: heard = %q, want %q", what, m.heard, want)
		}
		m.heard = ""
	}

	// With nothing focused the canvas hears a key.
	press("7")
	press("Enter")
	expect("nothing focused", "[7][Enter]")

	// A focused field types what the window leaves it, and nothing the window
	// stops; the window hears each key as one pressed while editing.
	c.Focus(m.field)
	press("a")
	press("x")
	press("b")
	expect("typing", "[a:e][x:e][b:e]")
	if m.field.Text != "ab" {
		t.Fatalf("the field holds %q, want %q", m.field.Text, "ab")
	}
	m.main.TypeKey(fynelayout.KeyPress{Key: "s", Ctrl: true})
	m.main.TypeKey(fynelayout.KeyPress{Key: "a", Ctrl: true})
	press("Backspace")
	expect("shortcuts", "[s^:e][a^:e][Backspace:e]")
	if m.field.Text != "a" {
		t.Fatalf("after a stopped Control+A and Backspace the field holds %q, want %q", m.field.Text, "a")
	}

	// The space bar presses a focused button once, unless the window stops it.
	// A button pressed gives up its focus, as fyne's do.
	c.Focus(m.next)
	press(" ")
	expect("space", "[ ]")
	c.Focus(m.next)
	m.stopping = true
	press(" ")
	expect("stopped space", "[ ]")
	if m.presses != 1 {
		t.Fatalf("presses = %d, want 1", m.presses)
	}

	// A stopped Tab moves no focus, from a widget or from nothing.
	press("Tab")
	expect("stopped Tab", "[Tab]")
	if c.Focused() != fyne.Focusable(m.next) {
		t.Fatalf("a stopped Tab moved focus to %T", c.Focused())
	}
	c.Unfocus()
	press("Tab")
	expect("stopped Tab, nothing focused", "[Tab]")
	if c.Focused() != nil {
		t.Fatalf("a stopped Tab with nothing focused focused %T", c.Focused())
	}
	m.stopping = false

	// Tab the window leaves moves focus, passing over the button written
	// focusable=false.
	var order []fyne.Focusable
	for range 3 {
		press("Tab")
		order = append(order, c.Focused())
	}
	expect("Tab", "[Tab][Tab:e][Tab]")
	want := []fyne.Focusable{m.field, m.next, m.field}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("focus %d after Tab = %T %p, want %T %p", i, order[i], order[i], want[i], want[i])
		}
	}

	// A test's key is heard as written, and one the window leaves goes where a
	// key pressed would, unheard a second time.
	stopped := 0
	m.mainKey(KeyPress{Key: "z"}, func() { stopped++ })
	expect("a test's key", "[z]")
	if m.field.Text != "az" || stopped != 0 {
		t.Fatalf("after a test's key the field holds %q and stop ran %d times", m.field.Text, stopped)
	}
	m.mainKey(KeyPress{Key: "x", Editing: true}, func() { stopped++ })
	expect("a test's stopped key", "[x:e]")
	if m.field.Text != "az" || stopped != 1 {
		t.Fatalf("after a test's stopped key the field holds %q and stop ran %d times", m.field.Text, stopped)
	}

	// Clicking the button written focusable=false leaves nothing focused.
	test.Tap(m.skip)
	if c.Focused() != nil || m.skips != 1 {
		t.Fatalf("after a click %T is focused and skips = %d", c.Focused(), m.skips)
	}
}
`

// A window's @key hears each key pressed in it before the focused widget
// does, through the widgets this platform builds and, with nothing focused,
// the canvas; a key it stops goes no further, Tab included. A button written
// `focusable=false` is one Tab passes over.
func TestAWindowsKeyRuns(t *testing.T) {
	runEmitted(t, "fyne-window-key-", []byte(generateFyneModelBuilt(t, windowKeySrc)), windowKeyDriver)
}
