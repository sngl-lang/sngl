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
component App() node {
    var rows list<string> = ["a", "b", "c"]
    var picked string = ""
    text(value="picked=" + picked)
    for var r = rows {
        button(text="row " + r, @click { picked = r })
    }
    button(text="unshift", @click { rows = ["z", "a", "b", "c"] })
}
window(title="H") { App() }
`

// A synthesized row that reads the owner's state through a call repaints.
//
// `decorate()` names none of the owner's state and reads it anyway, so the read
// stayed inside the synthesized component while passReactivity credited the
// owner's var with the node behind it -- the owner's click handler then patched
// a node id and a prop cell that only the factory declares. Lifting the call
// makes the slot the thing that re-fires, which is what a value crossing that
// boundary has always meant.
func TestSlotChildInstance_ARowReadingStateThroughACallRepaints(t *testing.T) {
	// Written on `main` rather than on a component `main` instantiates: an
	// inlined component's funcs are cloned without their Reads being repointed
	// at the clones' vars, so a call inside one is read as depending on
	// nothing and this leak's precondition never arises there.
	src := `
import . "sngl:ui"
window {
    var items list<string> = ["a", "b"]
    var suffix string = "!"
    func decorate() => suffix
    button(text="bang", @click { suffix = "?" })
    for var it = items {
        text(value="<" + it + decorate() + ">")
    }
}
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "<a!>") {
		t.Fatalf("the rows should render the call's value; body = %q", got)
	}

	page.MustElement("button").MustClick()
	page.MustWaitStable()

	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the click raised: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "<a?>") || !strings.Contains(got, "<b?>") {
		t.Errorf("every row should repaint what the call now returns; body = %q", got)
	}
}
