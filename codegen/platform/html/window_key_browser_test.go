//go:build !js

package html

import (
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

// A key pressed in the window that nothing in it takes is the window's `@key`,
// named as KeyboardEvent.key names it: a digit, an Enter. A key typed into a
// focused field is the field's, and the window does not hear it, nor one
// pressed with Control held. A button written `focusable=false` keeps the
// focus where it was when it is clicked, so the Enter after it is the
// window's rather than a second press of the button.
func TestWindow_AKeyNothingTakesIsTheWindows(t *testing.T) {
	src := `
import ui "sngl:ui"
component App root {
    var typed = ""
    var pressed = 0
    var field = ""
    ui.window(title="K", @key(k) { typed = typed + "[" + k + "]" }) {
        ui.text(value="typed {typed}.")
        ui.text(value="pressed {pressed}.")
        ui.button(text="key", focusable=false, @click { pressed = pressed + 1 })
        ui.input(:value=field)
    }
}
App()
`
	inBrowser(t, src, func(page *rod.Page) {
		body := func() string { return page.MustElement("body").MustText() }

		page.Keyboard.MustType(input.Digit7, input.Enter)
		page.MustWaitStable()
		if got := body(); !strings.Contains(got, "typed [7][Enter].") {
			t.Errorf("a 7 and an Enter pressed with nothing focused should reach @key; the page reads %q", got)
		}

		if err := page.Keyboard.Press(input.ControlLeft); err != nil {
			t.Fatal(err)
		}
		page.Keyboard.MustType(input.KeyA)
		if err := page.Keyboard.Release(input.ControlLeft); err != nil {
			t.Fatal(err)
		}
		page.MustWaitStable()
		if got := body(); !strings.Contains(got, "typed [7][Enter].") {
			t.Errorf("a key pressed with Control held should not reach @key; the page reads %q", got)
		}

		button := page.MustElementR("button", "key")
		if got := button.MustAttribute("tabindex"); got == nil || *got != "-1" {
			t.Errorf("a focusable=false button should be out of the Tab order; its tabindex is %v", got)
		}
		button.MustClick()
		page.MustWaitStable()
		if focused := page.MustEval(`() => document.activeElement === document.querySelector("button")`).Bool(); focused {
			t.Errorf("a focusable=false button took the focus when clicked")
		}
		page.Keyboard.MustType(input.Enter)
		page.MustWaitStable()
		if got := body(); !strings.Contains(got, "pressed 1.") || !strings.Contains(got, "typed [7][Enter][Enter].") {
			t.Errorf("the Enter after clicking a focusable=false button should be the window's and not press it again; the page reads %q", got)
		}

		field := page.MustElement("input")
		field.MustFocus()
		page.Keyboard.MustType(input.Digit8, input.Enter)
		page.MustWaitStable()
		if got := field.MustProperty("value").String(); got != "8" {
			t.Errorf("a key typed in a focused field should be the field's; it reads %q", got)
		}
		if got := body(); !strings.Contains(got, "typed [7][Enter][Enter].") {
			t.Errorf("keys typed in a focused field should not reach @key; the page reads %q", got)
		}
	})
}
