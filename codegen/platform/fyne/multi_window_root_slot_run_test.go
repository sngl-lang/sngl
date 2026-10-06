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
    ui.button #more(text="more", @click {
        items.push("c")
        notes.push("z")
    })
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
	check := func(when string, win fyne.CanvasObject, want string) {
		t.Helper()
		if got := rendered(win); got != want {
			t.Fatalf("%s: rendered %q, want %q", when, got, want)
		}
	}
	check("one built", m.one.Container, "one head,a 0,b 0,one foot,more")
	check("two built", m.two.Container, "two head,x,two foot,note")
	m.more_click_handler()
	check("one, after pushing in one", m.one.Container, "one head,a 0,b 0,c 0,one foot,more")
	check("two, written from one", m.two.Container, "two head,x,z,two foot,note")
	m.note_click_handler()
	check("two, after pushing in two", m.two.Container, "two head,x,z,y,two foot,note")
	check("one, untouched by two", m.one.Container, "one head,a 0,b 0,c 0,one foot,more")
}
`

// Each window renders the loop at the top of its body into its own container,
// between its own siblings, and a push in either window reaches every window
// that renders what it wrote.
func TestMultiWindowRootSlotsRun(t *testing.T) {
	runEmitted(t, "fyne-multi-window-root-slot-", []byte(generateFyneModelBuilt(t, multiWindowRootSlotSrc)), multiWindowRootSlotDriver)
}
