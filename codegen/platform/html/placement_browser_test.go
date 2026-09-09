//go:build !js

package html

import (
	"strings"
	"testing"
)

// A row that did not move is not touched.
//
// The rebuild a slot does by default removes every child and appends them all
// back. It is correct, and it is what a target without Features.InsertBefore
// still does, but it detaches nodes that did not move -- so focus, selection,
// scroll position and any running transition go with them, and appending one
// row to a list of a hundred touches a hundred and one nodes.
//
// The rows are component instances, which is where the saving currently is: an
// instance and the node it renders as are retained across the render, so the
// cursor can recognise them. A plain node is still built fresh each time, so
// nothing about it matches and it is placed like a new one -- reusing those is
// a separate piece of work, and this test would report it arriving.
//
// The assertion counts DOM mutations rather than looking for a symptom of them.
// Focus was the obvious instrument and it is the wrong one: the click that
// triggers the render moves focus to the button that received it, so the test
// would report its own interaction. A MutationObserver on the slot's container
// measures the thing the change is about.
func TestPlacement_AppendingTouchesOneNode(t *testing.T) {
	src := `
import . "sngl:ui"
component row(label string) node {
    var hits = 0
    text(value="row " + label + ":" + string(hits))
    button(text="hit " + label, @click { hits = hits + 1 })
}
component App() node {
    var rows list<string> = ["a", "b"]
    for var r = rows {
        row(label=r, key=r)
    }
    button(text="add", @click { rows = ["a", "b", "c"] })
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	page.MustEval(`() => {
		window.__mut = { added: 0, removed: 0 };
		const target = document.querySelector('[data-sngl-slot="0"]');
		new MutationObserver(records => {
			for (const r of records) {
				window.__mut.added += r.addedNodes.length;
				window.__mut.removed += r.removedNodes.length;
			}
		}).observe(target, { childList: true });
	}`)

	all := page.MustElements("button")
	all[len(all)-1].MustClick()
	page.MustWaitStable()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "row c") {
		t.Fatalf("the row should have been added; got %q", got)
	}

	added := page.MustEval(`() => window.__mut.added`).Int()
	removed := page.MustEval(`() => window.__mut.removed`).Int()
	if added != 1 || removed != 0 {
		t.Errorf("appending a row should add one node and remove none; added %d, removed %d", added, removed)
	}
}
