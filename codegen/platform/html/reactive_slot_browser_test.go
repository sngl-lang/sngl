//go:build !js

package html

import (
	"strings"
	"testing"
)

// Carousel shape: a reactive `if` inside a component nested in a window.
// Exactly one branch visible at a time. MustText() returns only VISIBLE text,
// so a hidden/removed branch must not appear.
func TestReactiveSlot_CarouselShowsOneAtATime(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component Car() ui {
    var a = 0
    button(text="next", @click { a = (a + 1) % 2 })
    stack {
        if a == 0 { text(value="ZERO") }
        if a == 1 { text(value="ONE") }
    }
}
window { window(title="H", href="/index.html") { Car() } }
`
	b := startComponent(t, src) // skips if no browser
	defer b.Close()
	page := b.Page()

	btn := page.MustElement("button")
	// Click REPEATEDLY: the slot must keep transitioning, not freeze after the
	// first update. (A stale-accumulator bug let the first click transition but
	// made the second throw in removeChild, freezing the carousel.)
	want := []string{"ZERO", "ONE", "ZERO", "ONE", "ZERO"} // initial + 4 clicks
	for step, expect := range want {
		txt := page.MustElement("body").MustText()
		absent := "ONE"
		if expect == "ONE" {
			absent = "ZERO"
		}
		if !strings.Contains(txt, expect) || strings.Contains(txt, absent) {
			t.Fatalf("step %d: want %q (not %q); body text=%q", step, expect, absent, txt)
		}
		if step < len(want)-1 {
			btn.MustClick()
			b.WaitStable(stableWait)
		}
	}
}

// Top-level reactive `if` (direct child of the component body, no enclosing element).
func TestReactiveSlot_TopLevelToggle(t *testing.T) {
	src := `
import . "sngl:ui"
import app "sngl:app"
app.window {
    var on = true
    button(text="t", @click { on = !on })
    if on { text(value="SHOWN") }
}
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if !strings.Contains(page.MustElement("body").MustText(), "SHOWN") {
		t.Fatalf("initial: want SHOWN visible")
	}
	page.MustElement("button").MustClick()
	b.WaitStable(stableWait)
	if strings.Contains(page.MustElement("body").MustText(), "SHOWN") {
		t.Fatalf("after toggle: SHOWN should be hidden")
	}
}
