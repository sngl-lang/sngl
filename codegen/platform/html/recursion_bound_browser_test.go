//go:build !js

package html

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/html/internal/webtest"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// A recursive component is built at run time, one host stack frame per level,
// so a base case that never arrives is not a wrong picture but a dead page.
// passRecursionDepth bounds it. These run the result: a compile-only check
// would not notice a bound that never fires.

// startBounded is startComponent with an error trap installed before the
// generated script runs. The bound's raise reaches an unbounded recursion's
// page as an uncaught throw -- which is the point, and which a test can only
// read by being listening when it happens.
func startBounded(t *testing.T, src string) *webtest.Browser {
	t.Helper()

	page := renderComponentHTML(t, src)
	const trap = `<script>window.__snglErr = "";` +
		`window.addEventListener("error", function (e) { window.__snglErr = String(e.message); });</script>`
	i := strings.Index(page, "<script")
	if i < 0 {
		t.Fatalf("generated page carries no script to trap: %q", page)
	}
	body := []byte(page[:i] + trap + page[i:])

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(body)
	})
	engine := webtest.New(mux)
	t.Cleanup(engine.Close)

	if testing.Short() {
		t.Skip("skipping browser test in -short mode")
	}
	browser, err := engine.StartHeadless(1280, 720)
	if err != nil {
		t.Skipf("browser unavailable: %v", err)
	}
	if err := browser.NavigateRaw(engine.BaseURL() + "/"); err != nil {
		browser.Close()
		t.Fatalf("navigate: %v", err)
	}
	_ = browser.WaitStable(stableWait)
	return browser
}

// A component that instantiates itself with no base case reaches the bound and
// says so. Without the bound the same page dies of "Maximum call stack size
// exceeded" -- a message naming nothing the author wrote, from a depth nobody
// chose.
func TestRecursionBound_UnboundedRecursionReportsTheBound(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component chain(n int) {
    text(value="[" + string(n) + "]")
    chain(n=n + 1)
}
component App() {
    var on = true
    button(text="go", @click { on = !on })
    if on {
        chain(n=0)
    }
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startBounded(t, src)
	defer b.Close()

	got := b.Page().MustEval("() => window.__snglErr").String()
	want := fmt.Sprintf("chain: recursion exceeded %d nested instances", lower.MaxRecursionDepth)
	if !strings.Contains(got, want) {
		t.Errorf("page error = %q; want it to name the bound (%q)", got, want)
	}
	if strings.Contains(got, "call stack") {
		t.Errorf("the host stack ran out before the bound did: %q", got)
	}
}

// A recursion that terminates well inside the bound raises nothing: the guard
// is a comparison the shallow case never fails.
//
// The depth is a #[construct] prop so the base case reads the parameter
// itself. An ordinary prop would be promoted to a reactive cell, and a
// recursive component with one of those does not survive its own page today --
// html emits the component's slot renderer at module scope, where the cell it
// reads does not exist. That is a bug of its own and not this bound's; the mark
// is how this test steps around it rather than asserting through it.
func TestRecursionBound_TerminatingRecursionIsUntouched(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
import . "sngl:macro"
component chain(#[construct] n int) {
    text(value="[" + string(n) + "]")
    if n > 0 {
        chain(n=n - 1)
    }
}
component App() {
    var d = 4
    button(text="go", @click { d = d + 1 })
    if d > 0 {
        chain(n=d)
    }
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startBounded(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("a terminating recursion raised: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[4]") {
		t.Errorf("the recursion should still render; got %q", got)
	}
}
