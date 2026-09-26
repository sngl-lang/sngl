package fyne

import "testing"

// rootSlotOrderSrc renders loops at the top of a window body and at the top of
// a component body built at run time, each between static siblings.
const rootSlotOrderSrc = `
import ui "sngl:ui"

component Row(label string) ui.node {
    var n = 0
    ui.button(text="{label} {n}", @click { n += 1 })
}

component Card(xs list<string>) ui.node {
    var open = true
    ui.text(value="[")
    for var x = xs {
        ui.text(value=x)
    }
    ui.text(value="|")
    if open {
        ui.text(value="open")
    }
    ui.text(value="]")
}

ui.window {
    var items = ["a", "b"]
    ui.text(value="header")
    for var it = items {
        Row(label=it)
    }
    ui.text(value="mid")
    for var it = items {
        Card(xs=[it])
    }
    ui.text(value="footer")
    ui.button #more(text="more", @click { items.push("c") })
}
`

const rootSlotOrderDriver = `package ui

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

func TestRootSlotsKeepTheirPlace(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()
	root := m.BuildUI()
	if want := "header,a 0,b 0,mid,[,a,|,open,],[,b,|,open,],footer,more"; rendered(root) != want {
		t.Fatalf("built %q, want %q", rendered(root), want)
	}
	m.moreClick()
	if want := "header,a 0,b 0,c 0,mid,[,a,|,open,],[,b,|,open,],[,c,|,open,],footer,more"; rendered(root) != want {
		t.Fatalf("after a push, rendered %q, want %q", rendered(root), want)
	}
}
`

// A reactive slot at the top of a body renders between the siblings it was
// written between, and a second BuildUI rebuilds rather than doubles them.
func TestRootSlotsKeepTheirPlaceRuns(t *testing.T) {
	runEmitted(t, "fyne-root-slot-order-", []byte(generateFyneModelBuilt(t, rootSlotOrderSrc)), rootSlotOrderDriver)
}
