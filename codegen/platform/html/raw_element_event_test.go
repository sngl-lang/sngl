package html

import (
	"strings"
	"testing"
)

// A raw element's event name is the DOM event name, so it must reach
// addEventListener unchanged.
//
// The wiring switch handled click, input and change and let its default case
// fall through to the click adder, so every other event compiled to a click
// listener: `@mouseover` type-checked, generated, and ran — on the wrong event,
// with nothing anywhere reporting it.
func TestRawElementEventKeepsItsName(t *testing.T) {
	src := `
import . "sngl://std"
import "sngl://platforms/html"
import "sngl://platforms/html"
output { none { html() } }
component main {
    var hits = 0
    vbox {
        html.div(textContent="hover me", @mouseover { hits = hits + 1 })
    }
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, `addEventListener("mouseover"`) {
		t.Errorf("@mouseover did not emit a mouseover listener:\n%s", out)
	}
	if strings.Contains(out, `addEventListener("click"`) {
		t.Errorf("@mouseover emitted a click listener:\n%s", out)
	}
}

// The three events that have their own adders keep it: click takes no event
// argument, input and change bind one.
func TestRawElementKnownEventsUnchanged(t *testing.T) {
	for _, tt := range []struct{ sngl, want string }{
		{"@click", `addEventListener("click"`},
		{"@input", `addEventListener("input"`},
		{"@change", `addEventListener("change"`},
	} {
		t.Run(tt.sngl, func(t *testing.T) {
			src := `
import . "sngl://std"
import "sngl://platforms/html"
import "sngl://platforms/html"
output { none { html() } }
component main {
    var hits = 0
    vbox {
        html.input(` + tt.sngl + ` { hits = hits + 1 })
    }
}
`
			if out := generateMainPage(t, src); !strings.Contains(out, tt.want) {
				t.Errorf("%s did not emit %s:\n%s", tt.sngl, tt.want, out)
			}
		})
	}
}
