//go:build !js

package html

import (
	"strings"
	"testing"
)

// A list row written as a plain node, not as a component. The handler closes
// over the iteration, which is what makes retaining the row a question at all.
const slotChildSrc = `
import . "sngl:ui"
import . "sngl:app"
component App() {
    var rows list<string> = ["a", "b", "c"]
    var picked string = ""
    text(value="picked=" + picked)
    for var r = rows {
        button(text="row " + r, @click { picked = r })
    }
    button(text="unshift", @click { rows = ["z", "a", "b", "c"] })
}
component main { window(title="H", href="/index.html") { App() } }
`

// A retained plain-node row reports the value it now renders.
//
// This is the failure the whole approach exists to avoid, asked of the rows the
// synthesis creates rather than of a component the program declared. Unshifting
// shifts every row's value by one, so the row that now renders "z" is the
// instance built for "a" -- and a handler kept from construction answers "a".
//
// It cannot go stale because the handler never moved into the row: it stayed in
// the render function where it was written, and travels in as an event whose
// prop cell reuseOrCreate writes again on every render.
func TestSlotChildInstance_RetainedRowIsNotStale(t *testing.T) {
	b := startComponent(t, slotChildSrc)
	defer b.Close()
	page := b.Page()

	all := page.MustElements("button")
	all[len(all)-1].MustClick() // unshift
	page.MustWaitStable()

	rows := page.MustElements("button")
	if got := rows[0].MustText(); !strings.Contains(got, "row z") {
		t.Fatalf("the first row should render the new head; got %q", got)
	}

	rows[0].MustClick()
	page.MustWaitStable()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picked=z") {
		t.Errorf("the retained row should report what it renders, not what it was built for; got %q", got)
	}
}

// The rows still render, in order, and each still reports its own value.
//
// The synthesis moves every value the row reads onto a prop and every handler
// out to the call site, so this is the assertion that neither rewiring lost
// track of which row is which.
func TestSlotChildInstance_EachRowReportsItsOwn(t *testing.T) {
	b := startComponent(t, slotChildSrc)
	defer b.Close()
	page := b.Page()

	rows := page.MustElements("button")
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "row a") || !strings.Contains(got, "row c") {
		t.Fatalf("every row should render; got %q", got)
	}

	rows[1].MustClick() // "row b"
	page.MustWaitStable()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picked=b") {
		t.Errorf("the second row should report its own value; got %q", got)
	}
}
