//go:build !js

package html

import (
	"strings"
	"testing"

	"github.com/go-rod/rod"
)

// effectLog is the bracketed text the page is showing.
func effectLog(body string) string {
	i := strings.Index(body, "[")
	j := strings.Index(body, "]")
	if i < 0 || j < i {
		return ""
	}
	return body[i : j+1]
}

// Two brackets rekeying EACH OTHER stop at the bound rather than running out of
// stack, and say so.
//
// This file used to hold two tests of one bracket rekeying ITSELF: one that it
// settled to a fixpoint through a helper, one that it stopped at the bound.
// Neither program compiles now -- an effect's own handler may not write what its
// own `on` reads -- so the fixpoint test is gone and this is what is left of the
// bound: A's mount writes what B is keyed on, B's mount writes what A is keyed
// on, and neither writes its own key, so no per-effect rule catches it. It is
// the better evidence anyway, since it is exactly the case the bound has to
// survive the checker's rule for.
//
// The count is 1024 rather than 512: a pass of the group runs both brackets, so
// the two handlers between them run twice per pass. The text is the count as it
// stood when the last mount patched it, which is before the settle raises -- the
// raise itself is pinned in testdata/effect_settle_sites.txtar, where its exact
// form on each target is visible.
func TestEffect_MutuallyRekeyingBracketsStopAtTheBound(t *testing.T) {
	src := `
import . "sngl:ui"
var (
    x = 0
    y = 0
    runs = 0
)
component App() node {
    effect(on=x, @mount {
        runs += 1
        y = y + 1
    })
    effect(on=y, @mount {
        runs += 1
        x = x + 1
    })
    text(value="[" + "{runs}" + "]")
}
window(title="H") { App() }
`
	inBrowser(t, src, func(page *rod.Page) {

		if got := effectLog(page.MustElement("body").MustText()); got != "[1024]" {
			t.Fatalf("runs after the bound = %s, want [1024]", got)
		}
	})
}
