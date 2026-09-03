//go:build !js

package html

import (
	"strings"
	"testing"
)

// A component the build cannot inline -- one under a dynamic `for`, or in a
// recursive cycle -- is instantiated at run time. These run the result.
//
// Until the factory landed, every one of these threw on load: the JS the
// CreateComponent dispatch emits is a call to `__cf_<name>`, and nothing
// anywhere defined it. Nothing caught that because the platform harness checks
// only that a fixture compiles, and no fixture had the shape.

// A component instantiated per element keeps its own state. The counter is
// what shows it: one cell shared between instances reads the same either way
// until two of them are clicked a different number of times.
func TestInstance_StatePerElement(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge(label string) {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit " + label, @click { hits = hits + 1 })
}
component App() {
    var names list<string> = ["a", "b"]
    for n = names {
        badge(label=n)
    }
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	body := page.MustElement("body").MustText()
	if !strings.Contains(body, "[a:0]") || !strings.Contains(body, "[b:0]") {
		t.Fatalf("both instances should render; got %q", body)
	}

	buttons := page.MustElements("button")
	if len(buttons) < 2 {
		t.Fatalf("want a button per instance, got %d", len(buttons))
	}
	buttons[0].MustClick()
	page.MustWaitStable()

	body = page.MustElement("body").MustText()
	if !strings.Contains(body, "[a:1]") {
		t.Errorf("the clicked instance should count; got %q", body)
	}
	if !strings.Contains(body, "[b:0]") {
		t.Errorf("its sibling shares no cell with it; got %q", body)
	}
}

// A prop crossing into a live instance reaches the leaf that reads it. This is
// what emitted `__n1.setAttribute("label", ...)` against a node that was not
// the instance, and changed nothing on the page at all.
//
// What it does NOT yet assert is that the instance survives the update. A slot
// re-fires for every reactive var its body reads, including one that only
// feeds an instance's prop, so the rebuild reaches the instance before the
// setter does and the state goes with it. Retaining the instance is keyed
// reconciliation, which is not built; when it is, this test gains the
// assertion that `hits` is unchanged.
func TestInstance_PropUpdateReachesTheLeaf(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge(label string) {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit", @click { hits = hits + 1 })
}
component App() {
    var (
        names list<string> = ["a"]
        tag = "x"
    )
    for n = names {
        badge(label=n + tag)
    }
    button(text="rename", @click { tag = "y" })
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ax:0]") {
		t.Fatalf("initial render; got %q", got)
	}
	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ay:") {
		t.Errorf("the prop should reach the leaf; got %q", got)
	}
}

// A recursive component is the other shape that cannot be inlined: the depth
// is not known until it runs.
func TestInstance_Recursive(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component chain(depth int) {
    text(value="<" + string(depth))
    if depth > 0 {
        chain(depth=depth - 1)
    }
}
component App() {
    chain(depth=3)
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	got := page.MustElement("body").MustText()
	for _, want := range []string{"<3", "<2", "<1", "<0"} {
		if !strings.Contains(got, want) {
			t.Errorf("recursion should reach %s; got %q", want, got)
		}
	}
	if strings.Contains(got, "<-1") {
		t.Errorf("recursion should stop at its own condition; got %q", got)
	}
}
