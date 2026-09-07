//go:build !js

package html

import (
	"strings"
	"testing"
)

// An event emitted from inside a lambda reaches the caller's handler.
//
// passInlinePure substitutes a component's `emit` with the block the call site
// wrote for that event, and the walk that finds those emits treated an
// assignment, a call statement, a local declaration and a return as leaves. A
// lambda written in one of their expressions is a body too, and it was the one
// body the walk never entered, so this page emitted
//
//	run(() => { emit("picked"); });
//
// -- a call to a function no target defines. The bundle parses, the click
// throws at run time, and the counter never moves, which is why this drives
// Chrome rather than reading the output: `emit` is a plausible-looking
// identifier and only running it says it is not there.
const lambdaEmitSrc = `
import . "sngl:ui"
import . "sngl:app"

func run(f func()) {
    f()
}

component chooser(@picked) {
    button(text="pick", @click {
        run(func() {
            picked()
        })
    })
}

component App() {
    var picks = 0

    chooser(@picked { picks += 1 })
    text(value="picks=" + string(picks))
}
component main { window(title="H", href="/index.html") { App() } }
`

func TestEmitInsideALambdaReachesTheHandler(t *testing.T) {
	b := startComponent(t, lambdaEmitSrc)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picks=0") {
		t.Fatalf("the page did not start at zero: %q", got)
	}
	page.MustElement("button").MustClick()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picks=1") {
		t.Errorf("the click did not reach the caller's handler; page reads %q", got)
	}
}
