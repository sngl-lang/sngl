//go:build !js

package html

import (
	"strings"
	"testing"
)

// An instance that is itself a registry holder: `nest` is recursive, so it
// survives inlining wherever it is written, and its own reactive `for` gives it
// a registry of the instances it holds. Dropping a row destroys the outer one.
const nestedRegistrySrc = `
import . "sngl:ui"
component nest(tag string, depth int, note func(string)) ui {
    var kids list<string> = depth > 0 ? [tag + "-"] : []
    effect(on=tag, @unmount { note(tag) })
    text(value="[" + tag + "]")
    for var k = kids {
        nest(tag=k, depth=depth - 1, note=note)
    }
}
component main ui {
    var rows list<string> = ["a", "b"]
    var log string = ""
    button(text="drop", @click { rows = ["a"] })
    text(value="log=" + log)
    for var r = rows {
        nest(tag=r, depth=1, note=func(s string) { log = log + s + ";" })
    }
}
`

// Destroying an instance destroys the instances it holds.
//
// DestroyComponent was emitted only from the holder's own slot render, so the
// registry a destroyed instance carried was simply dropped: every instance in
// it stayed alive with its brackets mounted, and on a target that counts
// references the reference was never released. Nothing outside could reach
// them -- the registry is a local of the factory's closure -- so the only place
// the teardown can happen is inside the instance's own __destroy.
func TestInstanceRegistry_DestroyReachesTheInstancesItHolds(t *testing.T) {
	b := startTrapped(t, nestedRegistrySrc)
	defer b.Close()
	page := b.Page()

	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	body := page.MustElement("body").MustText()
	for _, want := range []string{"[a]", "[a-]", "[b]", "[b-]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("every nested instance should render %s; body = %q", want, body)
		}
	}

	page.MustElement("button").MustClick()
	page.MustWaitStable()

	got := page.MustElement("body").MustText()
	if !strings.Contains(got, "b;") {
		t.Fatalf("the dropped row's own bracket should have unmounted; body = %q", got)
	}
	if !strings.Contains(got, "b-;") {
		t.Errorf("the instance the dropped row held should have been destroyed too; body = %q", got)
	}
	if !strings.Contains(got, "log=b-;b;") {
		t.Errorf("what an instance holds is destroyed before its own teardown runs; body = %q", got)
	}
}
