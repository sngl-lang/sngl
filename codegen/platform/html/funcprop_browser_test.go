//go:build !js

package html

import (
	"strings"
	"testing"
)

// A component the build cannot inline takes a func-typed prop: the way a row
// under a dynamic `for` calls back into the render that built it.
const funcPropSrc = `
import . "sngl:ui"
component row(label string, onpick func(string)) node {
    var hits = 0
    button(text="pick " + label + ":" + string(hits), @click { onpick(label) })
}
component App() node {
    var items list<string> = ["a", "b"]
    var picked string = ""
    text(value="picked=" + picked)
    for var item = items {
        row(label=item, onpick=func(s string) { picked = s })
    }
}
window(title="H") { App() }
`

// A handler handed to an instance at construction reaches what it writes.
//
// The reconcile has two branches, and the callback goes in by a different route
// in each: UpdateComponent takes it as a bare argument, and the create call
// takes it as a field of the props struct. The updater injection scanned a
// call's arguments only, so the reused row's callback repainted the view and
// the freshly built one wrote the state and updated nothing -- the first click
// on a list did nothing, and the same click after any re-render worked.
func TestFuncProp_FreshInstanceCallbackRepaints(t *testing.T) {
	b := startComponent(t, funcPropSrc)
	defer b.Close()
	page := b.Page()

	page.MustElements("button")[0].MustClick()
	page.MustWaitStable()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "picked=a") {
		t.Errorf("the callback should have repainted the view; got %q", got)
	}
}
