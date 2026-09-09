//go:build !js

package html

import (
	"strings"
	"testing"
)

// A package-level computed -- a zero-arg func returning a value -- is read on
// the first paint, so a name mismatch between its declaration and its call is
// not a cosmetic difference: the page throws before it renders anything.
//
// The expression-bodied computed was declared `function $doubled()` and called
// as `doubled()`. Only a run says so; the string is present in the output
// either way.
func TestComputed_IsCalledUnderTheNameItIsDeclaredWith(t *testing.T) {
	src := `
import . "sngl:ui"
import app "sngl:app"
var n = 3
func doubled() => n * 2
func blocky() int {
    var x = n + 1
    return x * 3
}
app.window {
    vbox {
        text(value="expr=" + string(doubled()))
        text(value="block=" + string(blocky()))
    }
}
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	body := page.MustElement("body").MustText()
	for _, want := range []string{"expr=6", "block=12"} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q; want it to carry %s", body, want)
		}
	}
}
