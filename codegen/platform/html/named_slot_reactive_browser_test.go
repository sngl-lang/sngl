//go:build !js

package html

import (
	"strings"
	"testing"
)

// A named slot's content is inlined like any other body, so a reactive `if`
// around the insertion renders it.
//
// The content used to arrive at passReactivity still spelled as the wrapper the
// caller wrote -- `text`, which on this platform is an override with a real
// body -- because passInlinePure walked a node's Children and Handlers and not
// its Slots. A reactive `if` then classified that wrapper as an instance to
// reconcile and asked for a setter no platform primitive has, so the build
// failed in lowering. The default slot was fine, because its content arrives as
// Children.
func TestNamedSlotInsideAReactiveIfRenders(t *testing.T) {
	src := `
import . "sngl:ui"

component q(body component) {
    var on = false

    button #t(text="toggle", @click { on = !on })
    vbox {
        if on {
            body {}
        }
    }
}

component main {
    q {
        component body {
            text(value="SUPPLIED")
        }
    }
}
`
	b := startComponent(t, src) // skips if no browser
	defer b.Close()
	page := b.Page()

	if txt := page.MustElement("body").MustText(); strings.Contains(txt, "SUPPLIED") {
		t.Fatalf("before the toggle the slot must not be rendered; body text=%q", txt)
	}
	page.MustElement("button").MustClick()
	b.WaitStable(stableWait)
	if txt := page.MustElement("body").MustText(); !strings.Contains(txt, "SUPPLIED") {
		t.Fatalf("after the toggle want SUPPLIED visible; body text=%q", txt)
	}
	// Back again: the slot has to keep transitioning, not render once and stick.
	page.MustElement("button").MustClick()
	b.WaitStable(stableWait)
	if txt := page.MustElement("body").MustText(); strings.Contains(txt, "SUPPLIED") {
		t.Fatalf("after toggling back the slot must be gone; body text=%q", txt)
	}
}

// The scoped form, which is what a component exposing a value to its caller
// needs: the insertion passes an argument and the population names it. The
// argument is read off the component's own state, so the rendered text also
// says the re-render carried the current value rather than the one the first
// render captured.
func TestScopedNamedSlotInsideAReactiveIfBindsItsArgument(t *testing.T) {
	src := `
import . "sngl:ui"

component q(ready component(int)) {
    var n = 0

    button #b(text="bump", @click { n = n + 1 })
    vbox {
        if n > 0 {
            ready(n) {}
        }
    }
}

component main {
    q {
        component ready(v) {
            text(value="N=" + string(v))
        }
    }
}
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if txt := page.MustElement("body").MustText(); strings.Contains(txt, "N=") {
		t.Fatalf("before the first bump nothing is ready; body text=%q", txt)
	}
	for _, want := range []string{"N=1", "N=2", "N=3"} {
		page.MustElement("button").MustClick()
		b.WaitStable(stableWait)
		if txt := page.MustElement("body").MustText(); !strings.Contains(txt, want) {
			t.Fatalf("want %q in the re-rendered slot; body text=%q", want, txt)
		}
	}
}
