//go:build !js

package html

import (
	"strings"
	"testing"
)

// A component's own method, called from a handler in that component's body.
// Inlining the component renames its vars per instance and clones the method
// against those names, so the handler has to reach the clone: a call names its
// callee on ir.Call.Func, which the inliner's rename walk did not repoint, so
// the handler called the original -- whose body writes vars that no longer
// exist under those names.
//
// Only a click says so. Both functions are in the output either way, and the
// wrong one is a plausible-looking name.
func TestInstanceMethod_HandlerCallsTheInstanceCopy(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component App ui {
    var n = 0
    func bump() {
        n = n + 1
    }
    vbox {
        text(value="n=" + string(n))
        button(text="bump", @click { bump() })
    }
}
window(title="H", href="/index.html") { App() }
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	page.MustElement("button").MustClick()
	_ = b.WaitStable(stableWait)

	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the click raised: %q", got)
	}
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "n=1") {
		t.Errorf("body = %q; want it to carry n=1 after one click", got)
	}
}
