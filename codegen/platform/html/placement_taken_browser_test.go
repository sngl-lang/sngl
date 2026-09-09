//go:build !js

package html

import (
	"testing"
)

// The placement fast path is actually taken for a slot child.
//
// slot_placement.go walks the desired order against the order already there and
// advances its cursor when `prev[i] is this node`. That test compares handles,
// so it can only ever match a node the render hands back the same object for.
// Every existing check of the pass reads the emitted shape or the rendered
// list, and both of those pass with the comparison permanently false -- which
// is what it was for a plain node, every row re-inserted on every render.
//
// So this asserts identity across a render rather than markup. The probe is an
// expando on the row's own DOM node plus the node reference itself: a rebuilt
// node is a different object and carries neither. Appending is the case with
// the least room for argument -- every existing row is still at its own index,
// so a render that reuses anything at all must reuse these.
//
// Focus would be the honest user-visible symptom and is the wrong instrument
// here for the reason TestPlacement_AppendingTouchesOneNode gives: the click
// that triggers the render takes focus itself, so the test would be reporting
// its own interaction.
func TestPlacement_FastPathIsTaken(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component App() ui {
    var rows list<string> = ["a", "b"]
    var picked string = ""
    text(value="picked=" + picked)
    for var r = rows {
        button(text="row " + r, @click { picked = r })
    }
    button(text="add", @click { rows = ["a", "b", "c"] })
}
window { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	stamped := page.MustEval(`() => {
		const slot = document.querySelector('[data-sngl-slot="0"]');
		window.__probe = slot.children[0];
		window.__probe.__snglKept = "kept";
		return slot.children.length;
	}`).Int()
	if stamped != 2 {
		t.Fatalf("the slot should hold two rows before the render; got %d", stamped)
	}

	all := page.MustElements("button")
	all[len(all)-1].MustClick() // "add" -- appends a third row
	page.MustWaitStable()

	got := page.MustEval(`() => {
		const slot = document.querySelector('[data-sngl-slot="0"]');
		if (slot.children.length !== 3) { return "wrong length: " + slot.children.length; }
		const now = slot.children[0];
		if (now !== window.__probe) { return "rebuilt: a different node object"; }
		if (now.__snglKept !== "kept") { return "rebuilt: the expando is gone"; }
		return "same";
	}`).String()

	if got != "same" {
		t.Errorf("appending a row should have kept the rows already placed; %s", got)
	}
}
