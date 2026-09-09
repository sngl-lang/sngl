//go:build !js

package html

import (
	"strings"
	"testing"
)

// A #[construct] prop written from state rebuilds the instance where it
// stands.
//
// The mark says the prop is read once while the instance is built, so there is
// no setter to write a new value into -- and at a position no reactive slot
// governs there is no render to rebuild from either. The build used to report
// that and stop. It destroys the instance and builds a fresh one from the new
// value now, putting the new root in before the old one comes out so the
// siblings keep their order.
//
// The assertion is the instance's own state, because a string match cannot
// tell a rebuilt instance from a patched one. `bump inner` moves the
// instance's `n` off its initial value; `bump` then changes the construct prop,
// and what the page shows afterwards is the fresh instance's `n` -- the new
// prop, not the value the old instance had reached.
func TestConstruct_APropWrittenFromStateRebuildsTheInstance(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
import . "sngl:macro"
component seeded(#[construct] start int, more = 0) ui {
    var n = start
    button(text="bump inner", @click { n = n + 5 })
    text(value="s:" + string(n))
    if more > 0 {
        seeded(start=start, more=more - 1)
    }
}
component App() ui {
    var k = 10
    text(value="before")
    button(text="bump", @click { k = k + 1 })
    seeded(start=k, more=0)
    text(value="after")
}
window(title="H", href="/index.html") { App() }
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	body := func() string { return page.MustElement("body").MustText() }
	click := func(label string) {
		for _, el := range page.MustElements("button") {
			if strings.TrimSpace(el.MustText()) == label {
				el.MustClick()
				page.MustWaitStable()
				return
			}
		}
		t.Fatalf("no button labelled %q; body = %q", label, body())
	}

	if got := body(); !strings.Contains(got, "s:10") {
		t.Fatalf("the instance should have been built from start=10; body = %q", got)
	}
	click("bump inner")
	if got := body(); !strings.Contains(got, "s:15") {
		t.Fatalf("the instance's own state should have moved; body = %q", got)
	}

	click("bump")
	got := body()
	if !strings.Contains(got, "s:11") {
		t.Errorf("the rebuilt instance should read start=11; body = %q", got)
	}
	if strings.Contains(got, "s:15") {
		t.Errorf("the old instance's state should be gone with the old instance; body = %q", got)
	}
	// One instance at the position, not two: the old root is removed, and the
	// new one goes in where it was rather than at the end of the flow.
	if n := strings.Count(got, "s:"); n != 1 {
		t.Errorf("the position should hold one instance, found %d; body = %q", n, got)
	}
	if i, j := strings.Index(got, "s:11"), strings.Index(got, "after"); i < 0 || j < 0 || i > j {
		t.Errorf("the rebuilt instance should still sit before its next sibling; body = %q", got)
	}
}
