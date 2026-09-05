//go:build !js

package html

import (
	"testing"
)

// A struct `on` is compared field by field in the browser too.
//
// This is testdata/effect_struct_key.sngl's compiled half, and html is where
// the divergence was widest: the settle emitted no comparison at all for a key
// whose type the three languages did not agree on, so the bracket was torn down
// and remounted on every settle. Even with a comparison emitted, `===` on two
// freshly built objects is false -- which is why the comparison the settle
// writes is over the fields and not over the value.
func TestEffect_AStructKeyComparesFieldByField(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
struct pair {
    a int
    b string
}
component App() {
    var (
        key pair = pair{a = 1, b = "x"}
        log list<string> = []
    )
    button(text="same", @click { key = pair{a = 1, b = "x"} })
    button(text="other", @click { key = pair{a = 1, b = "y"} })
    effect(on=key, @mount { log.push("m") }, @unmount { log.push("u") })
    text(value="[" + log.join(",") + "]")
}
component main { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if got := effectLog(page.MustElement("body").MustText()); got != "[m]" {
		t.Fatalf("initial log = %s, want [m]", got)
	}

	buttons := page.MustElements("button")
	if len(buttons) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(buttons))
	}

	// The same field values in a new object. Identity says these differ; the
	// language says they are one key.
	buttons[0].MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[m]" {
		t.Fatalf("after writing the same values back log = %s, want [m]", got)
	}

	buttons[1].MustClick()
	page.MustWaitStable()
	if got := effectLog(page.MustElement("body").MustText()); got != "[m,u,m]" {
		t.Fatalf("after changing a field log = %s, want [m,u,m]", got)
	}
}
