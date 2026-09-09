//go:build !js

package html

import (
	"strings"
	"testing"
)

// A row declaring an event, instantiated under a dynamic `for`. The parent
// subscribes at the call site, and its handler closes over the iteration --
// which is what makes the second test here the one that matters.
//
// The `var` is load-bearing: a component with no state of its own is pure, so
// passInlinePure substitutes it into the caller and the event goes with it.
// Only a component that survives inlining is instantiated at run time, which
// is the whole subject here.
const instanceEventSrc = `
import . "sngl:ui"
import . "sngl:app"
component row(label string, @pick) node {
    var seen = 0
    button(text="pick " + label + ":" + string(seen), @click { seen = seen + 1 pick(label) })
}
component App() node {
    var items list<string> = ["a", "b", "c"]
    var picked string = ""
    text(value="picked=" + picked)
    for var item = items {
        row(label=item, @pick { picked = item })
    }
    button(text="unshift", @click { items = ["z", "a", "b", "c"] })
}
window(title="H", href="/index.html") { App() }
`

// The event reaches the handler the call site wrote.
//
// An event has only ever worked by substitution: the inliner replaces the
// component's `emit(...)` with the block the call site wrote for it. An
// instance the build cannot inline keeps its emit, and codegen wrote that out
// as a call to `emit`, which nothing declares -- so the click threw a
// ReferenceError and the parent's handler was in the output nowhere at all.
func TestInstanceEvent_ReachesTheCallSiteHandler(t *testing.T) {
	b := startComponent(t, instanceEventSrc)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picked=") {
		t.Fatalf("the page should have rendered; got %q", got)
	}

	page.MustElements("button")[0].MustClick()
	page.MustWaitStable()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picked=a") {
		t.Errorf("clicking the first row should have reached the parent; got %q", got)
	}
}

// A retained row's handler acts on the value that row now renders.
//
// This is the failure the whole approach exists to avoid. The handler written
// at the call site closes over the iteration, so an instance that kept the one
// it was built with would report the row it was first built for -- silently,
// and only once a list is reordered. Unshifting shifts every row's value by
// one, so a stale handler answers with the label one position along.
//
// Retention is what makes the question live: the rows here are reused rather
// than rebuilt, which the placement cursor needs and which is exactly what
// would preserve a stale closure.
func TestInstanceEvent_RetainedRowIsNotStale(t *testing.T) {
	b := startComponent(t, instanceEventSrc)
	defer b.Close()
	page := b.Page()

	all := page.MustElements("button")
	all[len(all)-1].MustClick() // unshift: ["z", "a", "b", "c"]
	page.MustWaitStable()

	rows := page.MustElements("button")
	if got := rows[0].MustText(); !strings.Contains(got, "pick z") {
		t.Fatalf("the first row should render the new head; got %q", got)
	}

	rows[0].MustClick()
	page.MustWaitStable()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picked=z") {
		t.Errorf("the retained row should report what it renders, not what it was built for; got %q", got)
	}
}
