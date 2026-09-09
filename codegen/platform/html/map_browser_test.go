//go:build !js

package html

import (
	"strings"
	"testing"
)

// A map entry written by index reaches the map.
//
// JS is the one target where an entry write and an index write are different
// things: Go and Kotlin both spell it `m[k] = v`, and rendering that for a JS
// Map set a property on the object while the entry -- and `.size` -- stayed as
// they were. Reads were already correct, which is what kept it quiet: the
// write landed nowhere and the read returned the default.
//
// A browser test rather than a string assertion on the output, because what
// was wrong was not the spelling but what the spelling did.
func TestMapIndexWriteReachesTheMap(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component App() node {
    var counts map<string, int> = {}
    text(value="[" + string(counts.length()) + ":" + string(counts.get("a", 0)) + "]")
    button(text="put", @click { counts["a"] = 7 })
    button(text="bump", @click { counts["a"] += 5 })
}
window(title="H", href="/index.html") { App() }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[0:0]") {
		t.Fatalf("empty map; got %q", got)
	}

	buttons := page.MustElements("button")
	buttons[0].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[1:7]") {
		t.Errorf("after a write the entry should be there; got %q", got)
	}

	buttons[1].MustClick()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "[1:12]") {
		t.Errorf("a compound write reads the entry and sets it back; got %q", got)
	}
}
