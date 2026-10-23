//go:build !js

package html

import (
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

// A clickable card takes Enter and the space bar as a click, as the button its
// role says it is does. It is a div, which the browser gives a click and a
// place in the focus order (tabindex) but no key: a keyboard user reached the
// card and could not open it. A card that is not clickable takes neither.
func TestACardTakesEnterAndSpaceAsAClick(t *testing.T) {
	src := `
import ui "sngl:ui"
component App() ui.node {
    var hits = 0
    var plain = 0
    ui.card(clickable=true, @click { hits = hits + 1 }) {
        ui.text(value="hits {hits}")
    }
    ui.card(@click { plain = plain + 1 }) {
        ui.text(value="plain {plain}")
    }
}
ui.window(title="C") { App() }
`
	inBrowser(t, src, func(page *rod.Page) {
		card := page.MustElement(`[role="button"]`)
		card.MustFocus()
		page.Keyboard.MustType(input.Enter)
		page.Keyboard.MustType(input.Space)
		page.MustWaitStable()
		if got := page.MustElement("body").MustText(); !strings.Contains(got, "hits 2") {
			t.Errorf("Enter and Space on the focused card should click it twice; the page reads %q", got)
		}

		plain := page.MustElementR("div", "plain 0")
		plain.MustEval(`function() { this.setAttribute("tabindex", "-1"); this.focus(); }`)
		page.Keyboard.MustType(input.Enter)
		page.MustWaitStable()
		if got := page.MustElement("body").MustText(); !strings.Contains(got, "plain 0") {
			t.Errorf("a card that is not clickable took a key; the page reads %q", got)
		}
	})
}
