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
component badge(label string) ui {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit " + label, @click { hits = hits + 1 })
}
component App() ui {
    var names list<string> = ["a", "b"]
    for var n = names {
        badge(label=n)
    }
}
window(title="H", href="/index.html") { App() }
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
// It also asserts the instance survives it. The slot re-fires for every
// reactive var its body reads, including one that only feeds an instance's
// prop, so before the reconcile the rebuild reached the instance first and the
// state went with it. The counter separates the two failures: a rebuilt
// instance shows the new label with `hits` back at zero.
func TestInstance_PropUpdateReachesTheLeaf(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge(label string) ui {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit", @click { hits = hits + 1 })
}
component App() ui {
    var (
        names list<string> = ["a"]
        tag = "x"
    )
    for var n = names {
        badge(label=n + tag)
    }
    button(text="rename", @click { tag = "y" })
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ax:0]") {
		t.Fatalf("initial render; got %q", got)
	}
	// Click the instance's own button first, so the state the rename must not
	// disturb is distinguishable from a fresh instance's.
	page.MustElements("button")[0].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ax:1]") {
		t.Fatalf("after hit; got %q", got)
	}

	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ay:1]") {
		t.Errorf("the prop should reach the leaf and leave the state alone; got %q", got)
	}
}

// A recursive component is the other shape that cannot be inlined: the depth
// is not known until it runs.
func TestInstance_Recursive(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component chain(depth int) ui {
    text(value="<" + string(depth))
    if depth > 0 {
        chain(depth=depth - 1)
    }
}
component App() ui {
    chain(depth=3)
}
window(title="H", href="/index.html") { App() }
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

// Growing the list creates one instance and leaves the others holding what
// they held.
//
// This is what the reconcile is for. The slot rebuilds its nodes wholesale --
// right for what it draws, wrong for an instance, because a component's own
// `var`s live there. The counters are the evidence: rebuilt instances would
// all read zero, and a shared cell would read the same number in both.
func TestInstance_GrowingTheListKeepsTheOthers(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge(label string) ui {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit " + label, @click { hits = hits + 1 })
}
component App() ui {
    var names list<string> = ["a", "b"]
    for var n = names {
        badge(label=n)
    }
    button(text="grow", @click { names = ["a", "b", "c"] })
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	// Give the two instances different counts, so a rebuild and a shared cell
	// are both distinguishable from what should happen.
	buttons := page.MustElements("button")
	buttons[0].MustClick()
	page.MustWaitStable()
	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[a:1]") || !strings.Contains(got, "[b:2]") {
		t.Fatalf("counts before growing; got %q", got)
	}

	// The "grow" button is last, after one per instance.
	all := page.MustElements("button")
	all[len(all)-1].MustClick()
	page.MustWaitStable()

	got := page.MustElement("body").MustText()
	for _, want := range []string{"[a:1]", "[b:2]", "[c:0]"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s after growing; got %q", want, got)
		}
	}
}

// With `key=`, identity is the declared key rather than the position, so
// inserting at the front creates one instance and moves nobody's state.
//
// This is the case positional identity gets wrong, and gets wrong quietly:
// every instance after the insertion point shifts by one and keeps the
// previous element's state, so the page looks plausible and the counts sit on
// the wrong rows. The counters are the only way to see it.
func TestInstance_KeyedInsertAtFront(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge(label string) ui {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit " + label, @click { hits = hits + 1 })
}
component App() ui {
    var names list<string> = ["a", "b"]
    for var n = names {
        badge(label=n, key=n)
    }
    button(text="prepend", @click { names = ["z", "a", "b"] })
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[a:0]") || !strings.Contains(got, "[b:2]") {
		t.Fatalf("counts before the insert; got %q", got)
	}

	all := page.MustElements("button")
	all[len(all)-1].MustClick()
	page.MustWaitStable()

	// b keeps its own count. Under positional identity it would have taken
	// a's, because every row shifted by one.
	got := page.MustElement("body").MustText()
	for _, want := range []string{"[z:0]", "[a:0]", "[b:2]"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s after the insert; got %q", want, got)
		}
	}
}

// Two iterations claiming one key are two instances, not one.
//
// A duplicate key is the author's bug, but collapsing the rows is not the way
// to report it: the second iteration would reuse the first's instance, so one
// instance would render at two positions and clicking either would move both.
// The claim is suffixed instead, which is what the interpreter's iterationID
// does and for the same reason -- a keyed list has to mean the same thing on
// every target.
func TestInstance_DuplicateKeysAreDistinctInstances(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge(label string) ui {
    var hits = 0
    text(value="[" + label + ":" + string(hits) + "]")
    button(text="hit", @click { hits = hits + 1 })
}
component App() ui {
    var names list<string> = ["a", "a"]
    for var n = names {
        badge(label=n, key=n)
    }
    button(text="churn", @click { names = ["a", "a"] })
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	buttons := page.MustElements("button")
	if len(buttons) != 3 {
		t.Fatalf("want a row per iteration plus the churn button, got %d", len(buttons))
	}
	buttons[0].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[a:1]") {
		t.Fatalf("the clicked row should count; got %q", got)
	}

	// The re-render is what exposes it. On the first render the registry is
	// empty, so both iterations create and the second merely overwrites the
	// entry; only once the registry holds the key do both iterations find it
	// and reuse one instance for two rows.
	all := page.MustElements("button")
	all[len(all)-1].MustClick()
	page.MustWaitStable()

	got := page.MustElement("body").MustText()
	if !strings.Contains(got, "[a:1]") || !strings.Contains(got, "[a:0]") {
		t.Errorf("the two rows should still hold their own counts; got %q", got)
	}
}

// A component in a reactive `if` is an instance too, and re-entering the
// branch is a new one rather than the old one resumed. The counter is what
// says which: a persisted instance comes back showing the hit it was clicked
// before the branch went away.
//
// This is the semantics the inliner's reactive-context gate buys. Without the
// `if` head feeding that gate, the component inlines into the branch and its
// state cell lives on the one model, so the branch leaving and coming back
// changes nothing about it -- and an `if`-nested component would then persist
// where a `for`-nested one resets, making an observable difference depend on
// whether the inliner ran.
func TestInstance_BranchReentryResetsState(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component badge() ui {
    var hits = 0
    text(value="[" + string(hits) + "]")
    button(text="hit", @click { hits = hits + 1 })
}
component App() ui {
    var shown = true
    button(text="toggle", @click { shown = !shown })
    if shown {
        badge()
    }
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	toggle := page.MustElement("button")
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[0]") {
		t.Fatalf("initial render; got %q", got)
	}

	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[1]") {
		t.Fatalf("after hit; got %q", got)
	}

	toggle.MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); strings.Contains(got, "[1]") || strings.Contains(got, "[0]") {
		t.Fatalf("the branch is off, so the child should be gone; got %q", got)
	}

	toggle.MustClick()
	page.MustWaitStable()
	got := page.MustElement("body").MustText()
	if strings.Contains(got, "[1]") {
		t.Errorf("re-entering the branch should reset the child's state; got %q", got)
	}
	if !strings.Contains(got, "[0]") {
		t.Errorf("the child should be back; got %q", got)
	}
}
