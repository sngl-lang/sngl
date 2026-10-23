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
// card and could not open it. A card that is not clickable takes neither, and
// a key pressed in something inside a clickable card is that thing's: the
// card's listener hears it bubble, and taking it there swallowed a space typed
// in a field and stood in for a button's own Enter. Nor does the card hear
// the click a button inside it made, by mouse or by Enter, bubble up.
func TestACardTakesEnterAndSpaceAsAClick(t *testing.T) {
	src := `
import ui "sngl:ui"
component App() ui.node {
    var hits = 0
    var plain = 0
    var inner = 0
    var typed = ""
    ui.card(clickable=true, @click { hits = hits + 1 }) {
        ui.text(value="hits {hits}")
        ui.input(:value=typed)
        ui.button(text="inner {inner}", @click { inner = inner + 1 })
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

		field := page.MustElement("input")
		field.MustFocus()
		page.Keyboard.MustType(input.KeyA, input.Space, input.KeyB, input.Enter)
		page.MustWaitStable()
		if got := field.MustProperty("value").String(); got != "a b" {
			t.Errorf("a space typed in a field inside the card should be the field's; it reads %q", got)
		}
		if got := page.MustElement("body").MustText(); !strings.Contains(got, "hits 2") {
			t.Errorf("keys typed in a field inside the card should not click the card; the page reads %q", got)
		}
		// The button's own Enter clicks it, and the card does not hear that
		// click bubble up, nor a mouse's: a click a control inside the card
		// handled is the control's.
		page.MustElementR("button", "inner").MustFocus()
		page.Keyboard.MustType(input.Enter)
		page.MustWaitStable()
		if got := page.MustElement("body").MustText(); !strings.Contains(got, "inner 1") {
			t.Errorf("Enter on a button inside the card should click the button; the page reads %q", got)
		}
		page.MustElementR("button", "inner").MustClick()
		page.MustWaitStable()
		if got := page.MustElement("body").MustText(); !strings.Contains(got, "inner 2") || !strings.Contains(got, "hits 2") {
			t.Errorf("Enter and a click on a button inside the card should click the button and not the card; the page reads %q", got)
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
