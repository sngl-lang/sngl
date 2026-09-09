//go:build !js

package html

import (
	"strings"
	"testing"
)

// A #[js.native] declaration names a JavaScript identifier that already exists.
// Nothing about that is visible in a page that merely parses, which is why this
// drives Chrome: the failure it is written for emitted a *stub* under the
// global's own name --
//
//	function btoa(s) { return ""; }
//
// which compiles, runs, shadows the real global, and answers with nothing. A
// test that checked the bundle parsed, or grepped for the call, passed.
//
// Two declarations, because the two halves fail differently:
//
//   - `btoa` is spelled the way JavaScript spells it, so a stub emitted for it
//     shadows the global and the call finds the stub.
//   - `base64` is not, so nothing shadows anything. It fails the other way:
//     emitted as a call to `base64`, a name no JavaScript declares, when the
//     call is not routed through the scheme.
//
// btoa is the discriminator, because a stub's zero value and a real encode are
// different strings and the assertion cannot pass by accident.
//
// The calls are in a handler rather than in a prop. A prop html can render
// without running anything is rendered at build time, and a browser global is
// not a thing a build can call -- so a native belongs where JavaScript actually
// runs, which is also where the timer overrides put theirs.
const nativeSrc = `
import . "sngl:ui"
import . "sngl:app"
import js "sngl:language/js"

#[js.native("btoa")]
func btoa(s string) string

#[js.native("btoa")]
func base64(s string) string

component App() ui {
    var (
        same = "unset"
        renamed = "unset"
    )
    button(text="go", @click {
        same = btoa("hi")
        renamed = base64("hi")
    })
    text(value="same=" + same + " renamed=" + renamed)
}
window(title="H", href="/index.html") { App() }
`

func TestJSNative_CallsTheRealGlobal(t *testing.T) {
	b := startComponent(t, nativeSrc)
	defer b.Close()
	page := b.Page()

	if got := page.MustElement("body").MustText(); !strings.Contains(got, "same=unset") {
		t.Fatalf("the page did not start unset: %q", got)
	}
	page.MustElement("button").MustClick()

	// btoa("hi") is "aGk=" in every browser.
	body := page.MustElement("body").MustText()
	if !strings.Contains(body, "same=aGk=") {
		t.Errorf("a native named the way JavaScript names it did not reach the global; page reads %q", body)
	}
	if !strings.Contains(body, "renamed=aGk=") {
		t.Errorf("a native under a different SNGL name did not reach the global; page reads %q", body)
	}
}
