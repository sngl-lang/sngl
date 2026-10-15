//go:build !js

package html

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-rod/rod"

	"duckfam.us/sngl/internal/lower"
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
component chain(n int) node {
    text(value="[" + string(n) + "]")
    chain(n=n + 1)
}
component App() node {
    var on = true
    button(text="go", @click { on = !on })
    if on {
        chain(n=0)
    }
}
window(title="H") { App() }
`
	inBrowser(t, src, func(page *rod.Page) {

		got := page.MustEval("() => window.__snglErr").String()
		want := fmt.Sprintf("chain: recursion exceeded %d nested instances", lower.MaxRecursionDepth)
		if !strings.Contains(got, want) {
			t.Errorf("page error = %q; want it to name the bound (%q)", got, want)
		}
		if strings.Contains(got, "call stack") {
			t.Errorf("the host stack ran out before the bound did: %q", got)
		}
	})
}
