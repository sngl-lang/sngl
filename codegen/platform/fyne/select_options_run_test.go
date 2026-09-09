package fyne

import "testing"

// selectOptionsSrc binds a `select`'s choices to state a handler replaces, and
// starts with one of them chosen. The choice is what makes the claim sharp: a
// widget rebuilt to take new options would come back with nothing selected.
const selectOptionsSrc = `
import . "sngl:ui"
import app "sngl:app"

app.window {
    var opts list<string> = ["a", "b"]
    var pick = "b"
    vbox {
        select(options=opts, value=pick)
        button(text="more", @click { opts = ["a", "b", "c"] })
    }
}
`

// selectOptionsDriver taps the button and reads the live widget back.
//
// It holds the widget pointer across the change on purpose: the options have to
// reach *that* widget, because a Select rebuilt for them is a Select that lost
// its selection and its focus. Reading `Options` off a fresh one would pass
// either way.
const selectOptionsDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestSelectOptionsUpdateInPlace(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	sel := m.__n0
	if got := len(sel.Options); got != 2 {
		t.Fatalf("built with %d options, want 2", got)
	}
	if got := sel.Selected; got != "b" {
		t.Fatalf("built with %q selected, want %q", got, "b")
	}

	m.__n2.OnTapped()

	if sel != m.__n0 {
		t.Fatal("the select was rebuilt; a new widget has neither the selection nor the focus")
	}
	if got := len(sel.Options); got != 3 {
		t.Errorf("after the change the widget holds %d options (%v), want 3", got, sel.Options)
	}
	if got := sel.Selected; got != "b" {
		t.Errorf("the selection became %q, want it to survive as %q", got, "b")
	}
}
`

// TestSelectOptionsUpdateInPlace runs the emitted program: replacing the bound
// list has to reach widget.Select.SetOptions rather than being dropped.
func TestSelectOptionsUpdateInPlace(t *testing.T) {
	model := generateFyneModelBuilt(t, selectOptionsSrc)
	runEmitted(t, "fyne-select-options-", []byte(model), selectOptionsDriver)
}
