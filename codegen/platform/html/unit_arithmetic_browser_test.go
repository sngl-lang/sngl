//go:build !js

package html

import (
	"strings"
	"testing"
)

// A measurement has three independent bases, so its magnitudes reach the
// browser as a record and CSS is where they get spelled. This asserts the
// computed style the browser actually resolved, which is the only place the
// whole chain -- fold, per-base representation, CSS unit name -- is visible
// at once.
//
// Without the unit the suffix was dropped and IsSizeProp appended "px" to
// whatever was left, so `width = 2rem` resolved as 2 pixels.
func TestUnitArithmetic_MeasurementsAreSpelledAsCSSLengths(t *testing.T) {
	src := `
import . "sngl:ui"

window {
    vbox(style={width=2rem, padding=3px+4px, gap=1rem+2em}) {
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
	css := page.MustEval(`() => {
		const el = document.querySelector('div');
		const s = getComputedStyle(el);
		return JSON.stringify({padding: s.paddingTop, gap: s.rowGap, width: s.width});
	}`).String()

	// The browser resolves each against its own rules, which is what makes
	// these three assertions about the unit and not about the number:
	//   padding  3px + 4px = 7px, a length that resolves to itself
	//   gap      1rem + 2em = calc(1rem + 2em), 16 + 32 = 48px at the
	//            default font size -- a rem is the root's font, not 16em
	//   width    2rem = 32px -- "2px" would mean the unit was dropped
	for _, want := range []string{`"padding":"7px"`, `"gap":"48px"`, `"width":"32px"`} {
		if !strings.Contains(css, want) {
			t.Errorf("computed style = %s; want it to carry %s", css, want)
		}
	}
}
