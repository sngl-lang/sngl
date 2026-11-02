package fyne

import "testing"

const windowKeySrc = `
import ui "sngl:ui"

var typed = ""
var skips = 0

ui.window #main(title="Keys", @key(k) { typed = typed + "[" + k + "]" }) {
    ui.input #field()
    ui.button #skip(text="skip", focusable=false, @click { skips += 1 })
    ui.button #next(text="next")
}
`

const windowKeyDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"duckfam.us/sngl/pkg/go/fynelayout"
)

func TestKeysReachTheWindowNothingTakes(t *testing.T) {
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

	test.TypeOnCanvas(c, "7")
	m.mainKey("Enter")
	m.mainKey("+")
	if m.typed != "[7][Enter][+]" {
		t.Fatalf("typed = %q, want %q", m.typed, "[7][Enter][+]")
	}

	// A focused entry takes what is typed, and the window hears none of it.
	c.Focus(m.field)
	m.mainKey("x")
	m.mainKey("Backspace")
	m.mainKey("y")
	if m.field.Text != "y" || m.typed != "[7][Enter][+]" {
		t.Fatalf("with the entry focused: its text = %q, typed = %q", m.field.Text, m.typed)
	}

	// Tab passes over the button written focusable=false.
	c.Unfocus()
	var order []fyne.Focusable
	for range 3 {
		test.FocusNext(c)
		order = append(order, c.Focused())
	}
	want := []fyne.Focusable{m.field, m.next, m.field}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("focus %d after Tab = %T %p, want %T %p", i, order[i], order[i], want[i], want[i])
		}
	}

	// Clicking it leaves nothing focused, so Enter is the window's and does
	// not press it again.
	test.Tap(m.skip)
	if c.Focused() != nil {
		t.Fatalf("after a click %T is focused", c.Focused())
	}
	m.mainKey("Enter")
	if m.skips != 1 || m.typed != "[7][Enter][+][Enter]" {
		t.Fatalf("after a click and Enter: skips = %d, typed = %q", m.skips, m.typed)
	}
}
`

// A window's @key hears the keys nothing focused takes, through the canvas
// handlers fyne's driver hands them to, and a button written
// `focusable=false` is one Tab passes over.
func TestAWindowsKeyRuns(t *testing.T) {
	runEmitted(t, "fyne-window-key-", []byte(generateFyneModelBuilt(t, windowKeySrc)), windowKeyDriver)
}
