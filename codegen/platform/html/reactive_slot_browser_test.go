//go:build !js

package html

import (
	"strings"
	"testing"
)

// A TIMER-driven reactive `if` (the real carousel scenario) must re-render its
// slots when the timer mutates the dep — and the re-fire must target the same
// per-slot anchor the init call uses, not the threaded parentRef. This is a
// deterministic generated-JS assertion (no browser): the click-driven browser
// test above can't catch a timer-handler-only regression. Guards two bugs:
// (1) rewriteAndInject clobbering GenFunc so the timer handler got no re-fire,
// and (2) rewriteSlotCallsToAnchors not covering timer handlers so the re-fire
// pointed at the wrong parent node.
func TestReactiveSlot_TimerRefiresIntoAnchor(t *testing.T) {
	src := `
component Car() {
    var a = 0
    timer(interval=1000ms, @tick { a = (a + 1) % 2 })
    stack {
        if a == 0 { text(value="ZERO") }
        if a == 1 { text(value="ONE") }
    }
}
component main { window(title="H", href="/index.html") { Car() } }
`
	out := renderComponentHTML(t, src)
	// The timer tick handler must re-fire both slots into their anchors.
	tick := out[strings.Index(out, "_tick()"):]
	if i := strings.Index(tick, "}"); i >= 0 {
		tick = tick[:i]
	}
	for _, want := range []string{"__renderSlot0(__slotAnchor_0)", "__renderSlot1(__slotAnchor_1)"} {
		if !strings.Contains(tick, want) {
			t.Errorf("timer tick handler missing re-fire %q\n--- tick handler ---\n%s", want, tick)
		}
	}
	// Anchors must be the render-into target for the init calls too (same node).
	for _, want := range []string{`data-sngl-slot="0"`, `data-sngl-slot="1"`, "var __slotAnchor_0", "var __slotAnchor_1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing slot anchor wiring %q", want)
		}
	}
}

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
