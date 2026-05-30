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
component Car() {
    var a = 0
    button(text="next", @click { a = (a + 1) % 2 })
    stack {
        if a == 0 { text(value="ZERO") }
        if a == 1 { text(value="ONE") }
    }
}
component main { window(title="H", href="/index.html") { Car() } }
`
	b := startComponent(t, src) // skips if no browser
	defer b.Close()
	page := b.Page()

	txt := page.MustElement("body").MustText()
	if !strings.Contains(txt, "ZERO") || strings.Contains(txt, "ONE") {
		t.Fatalf("initial: want ZERO and not ONE; body text=%q", txt)
	}
	page.MustElement("button").MustClick()
	b.WaitStable(stableWait)
	txt = page.MustElement("body").MustText()
	if !strings.Contains(txt, "ONE") || strings.Contains(txt, "ZERO") {
		t.Fatalf("after click: want ONE and not ZERO; body text=%q", txt)
	}
}

// Top-level reactive `if` (direct child of the component body, no enclosing element).
func TestReactiveSlot_TopLevelToggle(t *testing.T) {
	src := `
component main {
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
