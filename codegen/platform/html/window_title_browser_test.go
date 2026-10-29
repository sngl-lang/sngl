//go:build !js

package html

import (
	"testing"

	"github.com/go-rod/rod"
)

// A window's title reading state follows it: the document's title is set as
// the script starts and again by each write the title reads. The page dropped
// every write to the document's props, the title's with them, so it kept the
// value it started with.
func TestWindow_ATitleReadingStateFollowsIt(t *testing.T) {
	src := `
import ui "sngl:ui"

var n = 0

ui.window(title="Count {n}") {
    ui.button(text="inc", @click { n += 1 })
}
`
	inBrowser(t, src, func(page *rod.Page) {
		if got := page.MustEval("() => window.__snglErr").String(); got != "" {
			t.Fatalf("the page raised on load: %q", got)
		}
		title := func() string { return page.MustEval("() => document.title").String() }
		if got := title(); got != "Count 0" {
			t.Fatalf("title on load = %q, want %q", got, "Count 0")
		}
		page.MustElement("button").MustClick()
		page.MustWaitStable()
		if got := title(); got != "Count 1" {
			t.Fatalf("title after a click = %q, want %q", got, "Count 1")
		}
	})
}
