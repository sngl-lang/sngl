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
import . "sngl:app"
component row(label string, onpick func(string)) {
    var hits = 0
    button(text="pick " + label + ":" + string(hits), @click { onpick(label) })
}
component App() {
    var items list<string> = ["a", "b"]
    var picked string = ""
    text(value="picked=" + picked)
    for var item = items {
        row(label=item, onpick=func(s string) { picked = s })
    }
}
component main { window(title="H", href="/index.html") { App() } }
`

// The page builds at all.
//
// Every prop a call site may omit carries a default, which the factory reads as
// `props.<name> ?? <default>`. An arrow function binds looser than `??`, so a
// func-typed prop's default closed the expression at its own `=>` and esbuild
// rejected the whole page. Nothing saw it: the platform harness compiles the
// fixtures it has and none had the shape.
func TestFuncProp_DefaultDoesNotBreakTheBundle(t *testing.T) {
	b := startComponent(t, funcPropSrc)
	defer b.Close()

	if got := b.Page().MustElement("body").MustText(); !strings.Contains(got, "pick a") {
		t.Fatalf("both instances should render; got %q", got)
	}
}
