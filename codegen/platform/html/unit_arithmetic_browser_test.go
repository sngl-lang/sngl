//go:build !js

package html

import (
	"strings"
	"testing"
)

// Unit arithmetic has to be asserted by running the page, because the wrong
// answer is a value rather than a shape. A unit value used to BE its source
// spelling, so `+` concatenated two strings: `100ms + 1s` evaluated to
// "100ms1s" and `int(...)` over it was Math.trunc of a string -- NaN. All of
// that compiled, loaded and painted.
//
// A duration is a plain number of ms because ms is duration's own base, which
// is what lets setInterval(f, interval) take one without any per-language
// table saying "JS wants milliseconds".
func TestUnitArithmetic_ADurationIsANumberOfMilliseconds(t *testing.T) {
	src := `
import . "sngl:ui"
import "sngl:time"

window {
    var short time.duration = 100ms
    var long time.duration = 1s
    vbox {
        text(value="sum=" + string(short + long))
        text(value="trunc=" + string(int(short + long)))
        text(value="scaled=" + string(int(short * 3)))
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
	for _, want := range []string{
		// Bare, so the display rule: the magnitude in duration's own base,
		// carrying that base's suffix. The cast beneath it is the number
		// alone, and the pair is what says the two are different questions
		// asked of one representation.
		"sum=1100ms",
		"trunc=1100", // a number, so int() is not NaN
		"scaled=300", // unit * scalar
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q; want it to carry %s", body, want)
		}
	}
}

// A measurement has five independent bases, so its magnitudes reach the
// browser as a record and CSS is where they get spelled. This asserts the
// computed style the browser actually resolved, which is the only place the
// whole chain -- fold, per-base representation, CSS unit name -- is visible
// at once.
//
// `pct` is spelled `%` in CSS because `%` is not a legal SNGL identifier.
// Without that rename the suffix was dropped and IsSizeProp appended "px" to
// whatever was left, so `width = 50pct` resolved as 50 pixels.
func TestUnitArithmetic_MeasurementsAreSpelledAsCSSLengths(t *testing.T) {
	src := `
import . "sngl:ui"

window {
    vbox(style={width=50pct, padding=3px+4px, gap=1rem+2em}) {
        text(value="x")
    }
}
`
	b := startTrapped(t, src)
	defer b.Close()

	page := b.Page()
	if got := page.MustEval("() => window.__snglErr").String(); got != "" {
		t.Fatalf("the page raised on load: %q", got)
	}
	// The parent is the page body, so 50% of it is not 50px unless the unit
	// was lost -- which is exactly the bug.
	css := page.MustEval(`() => {
		const el = document.querySelector('div');
		const s = getComputedStyle(el);
		return JSON.stringify({padding: s.paddingTop, gap: s.rowGap, width: s.width});
	}`).String()

	// The browser resolves each against its own rules, which is what makes
	// these three assertions about the unit and not about the number:
	//   padding  3px + 4px = 7px, a length that resolves to itself
	//   gap      1rem + 2em = 18em, which is 18*16 = 288px at the default
	//            font size -- "18px" would mean the em was dropped
	//   width    50pct = 50% of the 1280px test viewport = 640px --
	//            "50px" is what the missing `pct` -> `%` rename produced
	for _, want := range []string{`"padding":"7px"`, `"gap":"288px"`, `"width":"640px"`} {
		if !strings.Contains(css, want) {
			t.Errorf("computed style = %s; want it to carry %s", css, want)
		}
	}
}
