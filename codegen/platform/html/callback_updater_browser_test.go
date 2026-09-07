//go:build !js

package html

import (
	"strings"
	"testing"
)

// State written inside a callback updates what reads it.
//
// passReactivity splices an updater after every write to a tracked var, and the
// walk that finds those writes reached a callback only from a statement-level
// call and from the local a reconcile builds an instance into -- two
// hand-written descents. A callback held anywhere else was not walked, so
//
//	handle = run(func() { n = n + 1 })
//
// is an assignment, and the write inside it got no updater at all. The click
// ran, the state moved, and the page did not:
//
//	$0.addEventListener("click", function() {
//	  state.handle__inst0 = run(() => {
//	    state.n__inst0 = state.n__inst0 + 1;
//	  });
//	});
//
// `handle` is deliberately not read by any node. An assignment to a var the
// view does show gets its own updaters spliced after it, which refresh
// everything that statement's text depends on and hide the missing one -- so
// the shape that can fail is the one whose outer assignment nothing reads.
//
// Real Chrome, because the bundle parses and runs either way: the only
// difference is whether the span moves.
const callbackUpdaterSrc = `
import . "sngl:ui"
import . "sngl:app"

func run(f func()) int {
    f()
    return 7
}

component App() {
    var n = 0
    var handle = 0

    button(text="go", @click {
        handle = run(func() {
            n = n + 1
        })
    })
    text(value="n=" + string(n))
}
component main { window(title="H", href="/index.html") { App() } }
`

func TestAWriteInsideACallbackUpdatesWhatReadsIt(t *testing.T) {
	b := startComponent(t, callbackUpdaterSrc)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "n=0") {
		t.Fatalf("the page did not start at zero: %q", got)
	}
	page.MustElement("button").MustClick()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "n=1") {
		t.Errorf("the write inside the callback did not reach the span; page reads %q", got)
	}
}
