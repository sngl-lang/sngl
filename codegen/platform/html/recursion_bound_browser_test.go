//go:build !js

package html

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// A recursive component is built at run time, one host stack frame per level,
// so a base case that never arrives is not a wrong picture but a dead page.
// passRecursionDepth bounds it. These run the result: a compile-only check
// would not notice a bound that never fires.

// A component that instantiates itself with no base case reaches the bound and
// says so. Without the bound the same page dies of "Maximum call stack size
// exceeded" -- a message naming nothing the author wrote, from a depth nobody
// chose.
func TestRecursionBound_UnboundedRecursionReportsTheBound(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component chain(n int) ui {
    text(value="[" + string(n) + "]")
    chain(n=n + 1)
}
component App() ui {
    var on = true
    button(text="go", @click { on = !on })
    if on {
        chain(n=0)
    }
}
window(title="H", href="/index.html") { App() }
`
	b := startTrapped(t, src)
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
// The depth is an ordinary prop, promoted to a reactive cell like any other:
// re-firing this component's own slot from its setter is how a deeper level
// arrives when the caller raises the depth.
func TestRecursionBound_TerminatingRecursionIsUntouched(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component chain(n int) ui {
    text(value="[" + string(n) + "]")
    if n > 0 {
        chain(n=n - 1)
    }
}
component App() ui {
    var d = 4
    button(text="go", @click { d = d + 1 })
    if d > 0 {
        chain(n=d)
    }
}
window(title="H", href="/index.html") { App() }
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("a terminating recursion raised: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[4]") || !strings.Contains(got, "[0]") {
		t.Errorf("the recursion should render every level down to the base case; got %q", got)
	}
}

// A recursive component instantiated with a constant argument unrolls at build
// time, so no instantiation of it survives for the inliner to mark as a runtime
// instance -- and yet the declaration stays in the package, because it is its
// own caller. Everything the page needs of it is already written down as static
// HTML; nothing of its per-instance runtime should be written down at all.
//
// Before passComponentProps was gated on that mark, the promoted props made the
// component's own `if` reactive, so the page carried that component's slot
// renderer at module scope -- reading a dozen identifiers, `__prop_n` and
// `__root` among them, that only a factory declares, and calling a factory the
// build had no reason to emit. The script threw on load and took every handler
// on the page with it.
func TestRecursionBound_AnUnrolledRecursionCarriesNoInstanceRuntime(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component countdown(n int) ui {
    text(value="[" + string(n) + "]")
    if n > 0 {
        countdown(n=n - 1)
    }
}
component App() ui {
    var lbl = "go"
    button(text=lbl, @click { lbl = "went" })
    countdown(n=3)
}
window(title="H", href="/index.html") { App() }
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the unrolled recursion's page raised on load: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[3]") || !strings.Contains(got, "[0]") {
		t.Errorf("the unrolled recursion should render every level; got %q", got)
	}
	// The handler is the reason the throw matters: a page-scope ReferenceError
	// runs before any listener is registered, so nothing on the page responds.
	page.MustElement("button").MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "went") {
		t.Errorf("the button's handler never ran; body = %q", got)
	}
}
