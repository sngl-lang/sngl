//go:build !js

package html

import (
	"strings"
	"testing"
)

// The effect fixtures in testdata/ are asserted by the interpreter, and the
// platform harnesses only check that a fixture compiles. These run the lowered
// form: passEffect turns a bracket into a settle function reconciling a list of
// keys, and nothing else evaluates whether that list moves the way the
// interpreter's does.
//
// Each case reads the log out of the page, because the order the handlers ran
// in is the thing under test and a count cannot tell two orders apart.

// effectLog is the bracketed log the page is currently showing. Bracketed so
// an assertion can name the whole of it: the body's text carries the button
// labels too, and an empty log has to be distinguishable from a missing one.
func effectLog(body string) string {
	i := strings.Index(body, "[")
	j := strings.Index(body, "]")
	if i < 0 || j < i {
		return ""
	}
	return body[i : j+1]
}

// An effect in a reactive `if` takes its lifetime from the branch: the branch
// going away is the node leaving the tree, and coming back is a new lifetime
// rather than a resumed one. This is the case passEffect refused to lower.
func TestEffect_BranchIsTheLifetime(t *testing.T) {
	src := `
import . "sngl:ui"
component App() node {
    var (
        shown = true
        log list<string> = []
    )
    button(text="toggle", @click { shown = !shown })
    if shown {
        effect(@mount { log.push("in") }, @unmount { log.push("out") })
    }
    text(value="[" + log.join(",") + "]")
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	btn := page.MustElement("button")
	want := []string{"[in]", "[in,out]", "[in,out,in]", "[in,out,in,out]"}
	for step, expect := range want {
		if got := effectLog(page.MustElement("body").MustText()); got != expect {
			t.Fatalf("step %d: log = %s, want %s", step, got, expect)
		}
		if step < len(want)-1 {
			btn.MustClick()
			page.MustWaitStable()
		}
	}
}

// An effect in a `for` is one bracket per element, keyed by the element.
// Removing one element moves only its own bracket, which is what the list of
// keys being compared by index buys: the survivors compare equal and neither
// side of their bracket runs.
func TestEffect_OneBracketPerElement(t *testing.T) {
	src := `
import . "sngl:ui"
component App() node {
    var (
        items list<string> = ["a", "b"]
        log list<string> = []
    )
    button(text="drop", @click { items = ["a"] })
    for var it = items {
        effect(on=it, @mount(v) { log.push("+" + v) }, @unmount(v) { log.push("-" + v) })
    }
    text(value="[" + log.join(",") + "]")
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[+a,+b]" {
		t.Fatalf("initial log = %s, want [+a,+b]", got)
	}
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[+a,+b,-b]" {
		t.Fatalf("after drop log = %s, want [+a,+b,-b]", got)
	}
}

// Several lifetimes ending on one change end newest first. The teardown pass
// walks the running list backwards for this; walking it forwards produces
// exactly the same set of calls in exactly the wrong order, so only the log
// reports it. Mirrors testdata/effect_teardown_order.sngl, which asserts the
// same thing about the interpreter.
func TestEffect_TwoEndingsRunNewestFirst(t *testing.T) {
	src := `
import . "sngl:ui"
component App() node {
    var (
        items list<string> = ["a", "b", "c"]
        log list<string> = []
    )
    button(text="drop", @click { items = ["a"] })
    for var it = items {
        effect(on=it, @mount(v) { log.push("+" + v) }, @unmount(v) { log.push("-" + v) })
    }
    text(value="[" + log.join(",") + "]")
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[+a,+b,+c,-c,-b]" {
		t.Fatalf("after drop log = %s, want [+a,+b,+c,-c,-b]", got)
	}
}

// A key that is written but unchanged ends no lifetime: the bracket asks
// whether this is the same effect, not whether anything assigned to it.
func TestEffect_UnchangedKeyDoesNothing(t *testing.T) {
	src := `
import . "sngl:ui"
component App() node {
    var (
        n = 0
        log list<string> = []
    )
    button(text="same", @click { n = 0 })
    button(text="bump", @click { n = n + 1 })
    effect(on=n, @mount { log.push("m") }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	buttons := page.MustElements("button")
	if len(buttons) < 2 {
		t.Fatalf("want two buttons, got %d", len(buttons))
	}
	buttons[0].MustClick() // n = 0, the value it already held
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[m]" {
		t.Fatalf("after an unchanged write log = %s, want [m]", got)
	}
	buttons[1].MustClick() // n = 1
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[m,u,m]" {
		t.Fatalf("after a changed write log = %s, want [m,u,m]", got)
	}
}
