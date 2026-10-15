//go:build !js

package html

import (
	"strings"
	"testing"
)

// A factory's declarations live in the factory's closure, and the page's own
// emitters have one module scope. Five of them walked pkg.Components and
// answered "is this mine?" by hand, and three of them forgot to ask; each of
// these ran a page that named something only a factory declares.

// An effect inside a component that survives inlining settles once per
// instance, from inside the factory. The page must not call it: the name is
// not in scope there, and there is no instance for it to settle.
//
// bodyCalls collected the component's settle entry point from every component
// on the list, so the startup script called __effects0_settle() -- while the
// only definition, inside the factory, had been renamed __effects0_settle2 by
// the collision the page-scope declaration itself caused. Same for the
// teardown the page registered on pagehide.
func TestFactory_AnEffectInsideAFactoryIsNotCalledFromThePage(t *testing.T) {
	src := `
import . "sngl:ui"
component Row(label string) node {
    var log list<string> = []
    effect(on=label, @mount { log.push("+") }, @unmount { log.push("-") })
    text(value=label + "[" + log.join(",") + "]")
}
component App() node {
    var on = true
    button(text="toggle", @click { on = !on })
    if on { Row(label="x") }
}
window(title="H") { App() }
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "x[+]") {
		t.Errorf("the instance's effect should have mounted once; body = %q", got)
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); strings.Contains(got, "x[") {
		t.Errorf("toggling off should have destroyed the instance; body = %q", got)
	}
}
