package fyne

import "testing"

const slotOrderSrc = `
import . "sngl:ui"

var a = true
var items = ["x", "y"]

window {
    vbox {
        text(value="head")
        if a {
            text(value="A")
        }
        if true {
            text(value="B")
        }
        for var it = items {
            text(value=it)
        }
        text(value="tail")
        button #flip(text="flip", @click { a = !a })
        button #more(text="more", @click { items.push("z") })
    }
}
`

const slotOrderDriver = `package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func texts(o fyne.CanvasObject, out *[]string) {
	switch w := o.(type) {
	case *widget.Label:
		*out = append(*out, w.Text)
	case *fyne.Container:
		for _, c := range w.Objects {
			texts(c, out)
		}
	}
}

func TestASlotReRendersInPlace(t *testing.T) {
	test.NewApp()
	m := New()
	root := m.BuildUI()
	m.flipClick()
	m.flipClick()
	m.moreClick()
	var got []string
	texts(root, &got)
	if want := "head,A,B,x,y,z,tail"; strings.Join(got, ",") != want {
		t.Fatalf("rendered %q, want %q", strings.Join(got, ","), want)
	}
}
`

// A render slot re-renders where it was written, not after its siblings.
func TestASlotReRendersInPlaceRuns(t *testing.T) {
	runEmitted(t, "fyne-slot-order-", []byte(generateFyneModelBuilt(t, slotOrderSrc)), slotOrderDriver)
}
