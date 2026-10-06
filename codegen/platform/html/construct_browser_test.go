//go:build !js

package html

import (
	"strings"
	"testing"
)

// A #[construct] prop is read while the instance is built and never again, so
// a render describing a new value rebuilds the instance rather than patching
// it.
//
// `var n = start` is the shape that made the drop visible: the initializer runs
// once, so writing `start` afterwards reached a cell nobody read and the page
// changed nothing at all. The counter separates the three outcomes -- a patched
// instance keeps its count and shows the old start, a rebuilt one shows the new
// start with the count back at zero, and the bug shows neither.
func TestConstruct_ChangedValueRebuildsTheInstance(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:macro"
component badge(#[construct] start int, label string) node {
    var n = start
    text(value="[" + label + ":" + string(n) + "]")
    button(text="bump", @click { n = n + 1 })
}
component App() node {
    var (
        names list<string> = ["a"]
        base = 10
        tag = "x"
    )
    for var nm = names {
        badge(start=base, label=nm + tag)
    }
    button(text="rebase", @click { base = base + 100 })
    button(text="rename", @click { tag = "y" })
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ax:10]") {
		t.Fatalf("the initializer should read the prop; got %q", got)
	}
	// Move the instance off its initial state, so a rebuild is distinguishable
	// from a patch by more than the prop's own value.
	page.MustElements("button")[0].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ax:11]") {
		t.Fatalf("after bump; got %q", got)
	}

	// An ordinary prop is absorbed: the label changes and the state stays.
	page.MustElements("button")[2].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ay:11]") {
		t.Fatalf("an absorbable prop should patch in place; got %q", got)
	}

	// The construct prop is not: the instance is destroyed and built again, so
	// the initializer runs with the new value and the count is gone.
	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ay:110]") {
		t.Errorf("a construct prop should rebuild the instance; got %q", got)
	}
}

// A render that describes the same construct value keeps the instance. Without
// this, "cannot be written" would collapse into "rebuild on every render", and
// the state an instance exists to hold would go with it.
func TestConstruct_UnchangedValueKeepsTheInstance(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:macro"
component badge(#[construct] start int, label string) node {
    var n = start
    text(value="[" + label + ":" + string(n) + "]")
    button(text="bump", @click { n = n + 1 })
}
component App() node {
    var (
        names list<string> = ["a"]
        base = 10
        tag = "x"
    )
    for var nm = names {
        badge(start=base, label=nm + tag)
    }
    button(text="touch", @click { base = base })
    button(text="rename", @click { tag = "y" })
}
window(title="H") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	page.MustElements("button")[0].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ax:11]") {
		t.Fatalf("after bump; got %q", got)
	}
	// Both re-render the slot; neither changes what the instance was built
	// from.
	page.MustElements("button")[1].MustClick()
	page.MustWaitStable()
	page.MustElements("button")[2].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[ay:11]") {
		t.Errorf("an unchanged construct prop should keep the instance; got %q", got)
	}
}
