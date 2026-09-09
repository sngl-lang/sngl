//go:build !js

package html

import (
	"strings"
	"testing"
)

// A value narrowed by a null test is arithmetic, and stays so as the value
// changes underneath it.
//
// Narrowing is a judgement the checker makes, but it is not only a judgement:
// option<T> is a nullable representation in JavaScript and a pointer in Go, so
// the read a null test earns carries an unwrap conversion each language emits
// its own way. A checker test would pass with every one of them wrong. This
// one runs the emitted JavaScript in a browser and reads the number back.
func TestNarrowedOptionIsArithmeticInTheBrowser(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
component App() ui {
    var maybe option<int> = 3
    if maybe != null {
        text(value="[" + string(maybe * 2) + "]")
    }
    if maybe == null {
        text(value="[none]")
    } else {
        text(value="[" + string(maybe + 1) + "]")
    }
    button(text="clear", @click { maybe = null })
    button(text="set", @click { maybe = 21 })
}
component main ui { window(title="H", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	got := page.MustElement("body").MustText()
	if !strings.Contains(got, "[6]") || !strings.Contains(got, "[4]") {
		t.Fatalf("the then branch doubles and the else branch adds one; got %q", got)
	}

	buttons := page.MustElements("button")
	buttons[0].MustClick()
	page.MustWaitStable()
	got = page.MustElement("body").MustText()
	if !strings.Contains(got, "[none]") {
		t.Errorf("a null value takes the null branch; got %q", got)
	}
	if strings.Contains(got, "[6]") {
		t.Errorf("the narrowed branch must not render for a null value; got %q", got)
	}

	buttons[1].MustClick()
	page.MustWaitStable()
	got = page.MustElement("body").MustText()
	if !strings.Contains(got, "[42]") || !strings.Contains(got, "[22]") {
		t.Errorf("a new value re-runs both narrowed reads; got %q", got)
	}
}
