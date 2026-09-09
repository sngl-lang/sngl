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
import . "sngl:app"
component Row(label string) ui {
    var log list<string> = []
    effect(on=label, @mount { log.push("+") }, @unmount { log.push("-") })
    text(value=label + "[" + log.join(",") + "]")
}
component App() ui {
    var on = true
    button(text="toggle", @click { on = !on })
    if on { Row(label="x") }
}
window { window(title="H", href="/index.html") { App() } }
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

// A component the build could not inline, instantiated at a position no
// reactive slot governs, is still an instance: built once, placed once, never
// rebuilt. The page has to call its factory and put the root it returns where
// the node was written.
//
// Before this, the static renderer met the node and inlined the component's
// body as markup. That body had already been flattened into imperative
// statements for the factory, so the markup came out empty -- and the props and
// vars the renderer mirrored into `state` on the way past were read before
// their own `var` declarations, so each one initialised from undefined. The
// handler that updated the instance then reached for it through
// querySelector on an attribute nothing carried, and got null.
func TestFactory_AnInstanceAtAStaticPositionIsBuiltAndPlaced(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component chain(n int) ui {
    text(value="[" + string(n) + "]")
    if n > 0 { chain(n=n - 1) }
}
component App() ui {
    var d = 2
    button(text="deeper", @click { d = d + 1 })
    chain(n=d)
}
window { window(title="H", href="/index.html") { App() } }
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	got := page.MustElement("body").MustText()
	for _, want := range []string{"[2]", "[1]", "[0]"} {
		if !strings.Contains(got, want) {
			t.Errorf("the static instance should have rendered %s; body = %q", want, got)
		}
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[3]") {
		t.Errorf("the handler should have deepened the instance; body = %q", got)
	}
}
