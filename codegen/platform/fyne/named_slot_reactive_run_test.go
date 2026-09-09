package fyne

import "testing"

// namedSlotReactiveSrc is a named slot inside a reactive `if`.
//
// `q` holds state on purpose. A pure component is substituted into its caller
// before passInlinePure ever looks at a slot, so the content is walked as
// ordinary children and the defect does not appear -- which is why the shape
// that has to be tested is the stateful one.
const namedSlotReactiveSrc = `
import . "sngl:ui"
import app "sngl:app"

component q(body component) ui {
    var on = false

    button #t(text="toggle", @click { on = !on })
    vbox {
        if on {
            body {}
        }
    }
}

app.window {
    q {
        component body {
            text(value="SUPPLIED")
        }
    }
}
`

// The scoped form: the insertion passes a value and the population names it.
const scopedNamedSlotReactiveSrc = `
import . "sngl:ui"
import app "sngl:app"

component q(ready component(int)) ui {
    var n = 0

    button #b(text="bump", @click { n = n + 1 })
    vbox {
        if n > 0 {
            ready(n) {}
        }
    }
}

app.window {
    q {
        component ready(v) {
            text(value="N=" + string(v))
        }
    }
}
`

// namedSlotReactiveDriver walks what the program renders, before and after the
// toggle. The label is the whole claim: the slot's content has to reach the
// widget tree, and reach it again with the new value on a re-render.
const namedSlotReactiveDriver = `package ui

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
	case *widget.Button:
		*out = append(*out, w.Text)
	case *fyne.Container:
		for _, c := range w.Objects {
			texts(c, out)
		}
	}
}

func rendered(m *Model) string {
	var got []string
	texts(m.BuildUI(), &got)
	return strings.Join(got, ",")
}

func TestTheNamedSlotRendersWhenTheReactiveIfTurnsOn(t *testing.T) {
	test.NewApp()
	m := New()

	if got := rendered(m); strings.Contains(got, "SUPPLIED") {
		t.Fatalf("before the toggle rendered = %q, want no SUPPLIED", got)
	}
	m.tClick()
	if got := rendered(m); !strings.Contains(got, "SUPPLIED") {
		t.Fatalf("after the toggle rendered = %q, want SUPPLIED in it", got)
	}
	m.tClick()
	if got := rendered(m); strings.Contains(got, "SUPPLIED") {
		t.Fatalf("after toggling back rendered = %q, want no SUPPLIED", got)
	}
}
`

const scopedNamedSlotReactiveDriver = `package ui

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
	case *widget.Button:
		*out = append(*out, w.Text)
	case *fyne.Container:
		for _, c := range w.Objects {
			texts(c, out)
		}
	}
}

func rendered(m *Model) string {
	var got []string
	texts(m.BuildUI(), &got)
	return strings.Join(got, ",")
}

func TestTheScopedNamedSlotCarriesItsArgument(t *testing.T) {
	test.NewApp()
	m := New()

	if got := rendered(m); strings.Contains(got, "N=") {
		t.Fatalf("before the first bump rendered = %q, want nothing ready", got)
	}
	for _, want := range []string{"N=1", "N=2", "N=3"} {
		m.bClick()
		if got := rendered(m); !strings.Contains(got, want) {
			t.Fatalf("rendered = %q, want %q in it", got, want)
		}
	}
}
`

// A named slot inside a reactive `if` reaches the widget tree.
//
// passInlinePure walked a node's Children and Handlers but not its Slots, so
// the content arrived at passReactivity still spelled as `text` -- an override
// with a real body on this platform -- and the reactive block classified it as
// an instance to reconcile, asking for a setter no primitive has. The build
// failed in lowering, on fyne, gtk4 and html alike.
func TestANamedSlotInAReactiveIfRuns(t *testing.T) {
	runEmitted(t, "fyne-named-slot-", []byte(generateFyneModelBuilt(t, namedSlotReactiveSrc)), namedSlotReactiveDriver)
}

func TestAScopedNamedSlotInAReactiveIfRuns(t *testing.T) {
	runEmitted(t, "fyne-scoped-slot-", []byte(generateFyneModelBuilt(t, scopedNamedSlotReactiveSrc)), scopedNamedSlotReactiveDriver)
}
