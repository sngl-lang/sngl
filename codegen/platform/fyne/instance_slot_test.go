package fyne

import (
	"testing"
)

// slotInstanceSrc is a component the build cannot inline -- it sits under a
// dynamic `for` -- whose own body holds a reactive block. Two things about an
// instance meet here that meet nowhere else: the nodes a slot renderer
// creates belong to the record, and the container that renderer renders into
// is what the instance has to render as.
const slotInstanceSrc = `
import . "sngl:ui"
import app "sngl:app"

component card(name = "") node {
    var open = true
    button(text=name, @click { open = !open })
    if open {
        text(value="body of " + name)
    }
}

app.window {
    var items list<string> = ["a", "b"]
    for var it = items {
        card(name=it)
    }
}
`

// The widgets a factory component's slot renderer creates are discovered from
// that component's funcs, not only from its body.
//
// collectNodes walked every component's body and every func the Model owns,
// which is not the same set: a component the build could not inline keeps its
// slot renderer on itself. So the label inside the reactive block had no spec,
// OnCreateNode emitted nothing for it, and the renderer referenced a variable
// no statement declared.
//
//	./model.go:121:17: undefined: __n1
func TestInstanceSlotWidgetsAreDiscovered(t *testing.T) {
	runEmitted(t, "fyne-slot-inst-", generateForFyne(t, slotInstanceSrc), slotInstanceDriver)
}

// slotInstanceDriver walks the instance's own widget tree, which is the claim:
// the reactive block's label has to be reachable from what the instance
// renders as.
const slotInstanceDriver = `package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func texts(o fyne.CanvasObject, out *[]string) {
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

func TestTheSlotSubtreeIsReachableFromTheInstanceRoot(t *testing.T) {
	test.NewApp()
	m := New()
	m.BuildUI()

	if got := len(m.__inst0_live); got != 2 {
		t.Fatalf("expected 2 live instances, got %d", got)
	}
	var got []string
	texts(m.__inst0_live[0].Root, &got)
	want := []string{"a", "body of a"}
	if len(got) != len(want) {
		t.Fatalf("instance tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("instance tree = %v, want %v", got, want)
		}
	}
}
`
