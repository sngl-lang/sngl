package fyne

import "testing"

// multiWindowRootSlotSrc has two windows, each with a loop at the top of its
// body between static siblings.
const multiWindowRootSlotSrc = `
import ui "sngl:ui"

component Row(label string) ui.node {
    var n = 0
    ui.button(text="{label} {n}", @click { n += 1 })
}

var items = ["a", "b"]
var notes = ["x"]

ui.window #one(title="One") {
    ui.text(value="one head")
    for var it = items {
        Row(label=it)
    }
    ui.text(value="one foot")
    ui.button #more(text="more", @click { items.push("c") })
}

ui.window #two(title="Two") {
    ui.text(value="two head")
    for var it = notes {
        ui.text(value=it)
    }
    ui.text(value="two foot")
    ui.button #note(text="note", @click { notes.push("y") })
}
`

const multiWindowRootSlotDriver = `package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func texts(o fyne.CanvasObject, out *[]string) {
	if !o.Visible() {
		return
	}
	switch w := o.(type) {
	case *widget.Label:
		*out = append(*out, w.Text)
	case *widget.Button:
		*out = append(*out, w.Text)
	case *fyne.Container:
		for _, c := range w.Objects {
			texts(c, out)
		}
	}
}

func rendered(o fyne.CanvasObject) string {
	var got []string
	texts(o, &got)
	return strings.Join(got, ",")
}

func TestEachWindowKeepsItsRows(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()
	root := m.BuildUI()
	check := func(when, want string) {
		t.Helper()
		if got := rendered(root); got != want {
			t.Fatalf("%s: rendered %q, want %q", when, got, want)
		}
	}
	check("built", "one head,a 0,b 0,one foot,more")
	m.more_click_handler()
	check("after pushing in one", "one head,a 0,b 0,c 0,one foot,more")
	m.navigate("two")
	check("window two", "two head,x,two foot,note")
	m.note_click_handler()
	check("after pushing in two", "two head,x,y,two foot,note")
	m.navigate("one")
	check("back in one", "one head,a 0,b 0,c 0,one foot,more")
}
`

// Each window renders its top-level slot into a root of its own, so its rows
// show between its own siblings and a push reaches the window it belongs to.
func TestMultiWindowRootSlotsRun(t *testing.T) {
	runEmitted(t, "fyne-multi-window-root-slot-", []byte(generateFyneModelBuilt(t, multiWindowRootSlotSrc)), multiWindowRootSlotDriver)
}
