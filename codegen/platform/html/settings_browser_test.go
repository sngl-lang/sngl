//go:build !js

package html

import (
	"strings"
	"testing"

	"github.com/go-rod/rod"
)

// A kept setting outlives the page: a reload mounts it from the value the page
// left in localStorage, where its default would say 0. A test page keeps
// settings in memory instead, so this drives the page itself.
func TestSettings_AKeptValueOutlivesAReload(t *testing.T) {
	src := `
import settings "sngl:settings"
import ui "sngl:ui"

var count option[int] = null

ui.window(title="K") {
    settings.kept(name="count", default=0, :value=count)
    if count != null {
        ui.text(value="count {count}")
    }
    ui.button(text="bump", @click {
        var n = count
        if n != null {
            count = n + 1
        }
    })
}
`
	inBrowser(t, src, func(page *rod.Page) {
		if got := page.MustEval("() => window.__snglErr").String(); got != "" {
			t.Fatalf("the page raised on load: %q", got)
		}
		body := func() string { return page.MustElement("body").MustText() }
		if got := body(); !strings.Contains(got, "count 0") {
			t.Fatalf("a first visit starts at the default; body = %q", got)
		}
		page.MustElement("button").MustClick()
		page.MustWaitStable()
		if got := body(); !strings.Contains(got, "count 1") {
			t.Fatalf("the click should have counted; body = %q", got)
		}
		page.MustReload()
		page.MustWaitStable()
		if got := body(); !strings.Contains(got, "count 1") {
			t.Errorf("the reloaded page should mount the kept count; body = %q", got)
		}
	})
}
