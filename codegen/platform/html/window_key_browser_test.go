//go:build !js

package html

import (
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

// keysApp is a window whose `@key` writes down each key it hears, with the
// modifiers and `editing` it is handed, and stops a Tab, an "x", one with
// Control held, and every key while `stopping` holds. Its button and its card
// write down their clicks beside them, so the order the two heard a key in
// shows: a card clicks as its own keydown listener hears an Enter, which a
// window hearing the keydown as it bubbled up would hear after it.
const keysApp = `
import ui "sngl:ui"
component App root {
    var heard = ""
    var field = ""
    var stopping = false
    ui.window(title="K", @key(e, stop) {
        heard = heard + "[" + e.key + (e.ctrl ? "+ctrl" : "") + (e.shift ? "+shift" : "") + (e.editing ? "+editing" : "") + "]"
        if stopping || e.ctrl || e.key == "x" || e.key == "Tab" {
            stop()
        }
    }) {
        ui.text(value="heard {heard}.")
        ui.button(text="press", @click { heard = heard + "(click)" })
        ui.button(text="stop", focusable=false, @click { stopping = !stopping })
        ui.input(:value=field)
        ui.card(clickable=true, @click { heard = heard + "(card)" }) {
            ui.text(value="card")
        }
    }
}
App()
`

// heardIs fails t unless the page's `heard` line reads want.
func heardIs(t *testing.T, page *rod.Page, want, why string) {
	t.Helper()
	page.MustWaitStable()
	if got := page.MustElement("body").MustText(); !strings.Contains(got, "heard "+want+".") {
		t.Errorf("%s: want heard %q; the page reads %q", why, want, got)
	}
}

// The window hears a key before the button or the card with the focus does,
// and a key it does not stop goes on to press it, once. One it stops presses
// nothing: neither an Enter, which clicks as the key goes down, nor a space,
// which clicks a button as it comes up, so the keyup after it is stopped too.
// A button written `focusable=false` keeps the focus where it was when it is
// clicked.
func TestWindow_HearsAKeyBeforeTheFocusedButton(t *testing.T) {
	inBrowser(t, keysApp, func(page *rod.Page) {
		page.MustEval(`() => { window.ups = 0; document.addEventListener("keyup", () => window.ups++); }`)
		ups := func() int { return page.MustEval(`() => window.ups`).Int() }

		page.MustElementR("button", "press").MustFocus()
		page.Keyboard.MustType(input.Enter)
		heardIs(t, page, "[Enter](click)", "an Enter on a focused button the window does not stop should reach @key first and then press the button once")
		page.Keyboard.MustType(input.Space)
		heardIs(t, page, "[Enter](click)[ ](click)", "a space on a focused button the window does not stop should reach @key first and then press the button once")
		if got := ups(); got != 2 {
			t.Errorf("the keyup after a key the window does not stop should go on; %d of 2 did", got)
		}

		stop := page.MustElementR("button", "stop")
		if got := stop.MustAttribute("tabindex"); got == nil || *got != "-1" {
			t.Errorf("a focusable=false button should be out of the Tab order; its tabindex is %v", got)
		}
		stop.MustClick()
		page.MustWaitStable()
		if focused := page.MustEval(`() => document.activeElement === [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === "press")`).Bool(); !focused {
			t.Errorf("clicking a focusable=false button moved the focus off the button that had it")
		}
		page.Keyboard.MustType(input.Enter, input.Space)
		heardIs(t, page, "[Enter](click)[ ](click)[Enter][ ]", "an Enter and a space the window stops should press no button")
		if got := ups(); got != 2 {
			t.Errorf("the keyup after a key the window stops should be stopped too; %d of the 2 stopped went on", got-2)
		}

		card := page.MustElement("[role=button]")
		card.MustFocus()
		page.Keyboard.MustType(input.Enter)
		heardIs(t, page, "[Enter](click)[ ](click)[Enter][ ][Enter]", "an Enter the window stops should press no card")
		stop.MustClick()
		card.MustFocus()
		page.Keyboard.MustType(input.Enter)
		heardIs(t, page, "[Enter](click)[ ](click)[Enter][ ][Enter][Enter](card)", "an Enter on a focused card should reach @key before the card's own keydown listener")
	})
}

// A key pressed in a focused field is the window's first, handed `editing`,
// and goes on to type where the window does not stop it: a stopped one types
// nothing. A key with Control held is its own character, with `ctrl`; Control
// alone is not a key the window hears.
func TestWindow_HearsAKeyBeforeTheFocusedField(t *testing.T) {
	inBrowser(t, keysApp, func(page *rod.Page) {
		field := page.MustElement("input")
		field.MustFocus()
		page.Keyboard.MustType(input.KeyA, input.KeyX)
		if err := page.Keyboard.Press(input.ControlLeft); err != nil {
			t.Fatal(err)
		}
		page.Keyboard.MustType(input.KeyS)
		if err := page.Keyboard.Release(input.ControlLeft); err != nil {
			t.Fatal(err)
		}
		heardIs(t, page, "[a+editing][x+editing][s+ctrl+editing]", "keys pressed in a focused field should reach @key with editing, and Control+S as s with ctrl")
		if got := field.MustProperty("value").String(); got != "a" {
			t.Errorf("a key the window does not stop should type into the field, and one it stops should not; it reads %q", got)
		}

		page.MustEval(`() => document.activeElement.blur()`)
		page.Keyboard.MustType(input.Digit7)
		heardIs(t, page, "[a+editing][x+editing][s+ctrl+editing][7]", "a key pressed with nothing focused should reach @key without editing")
	})
}

// A Tab is the window's to hear as well, and one it stops moves no focus.
func TestWindow_AStoppedTabMovesNoFocus(t *testing.T) {
	inBrowser(t, keysApp, func(page *rod.Page) {
		page.MustElementR("button", "press").MustFocus()
		page.Keyboard.MustType(input.Tab)
		heardIs(t, page, "[Tab]", "a Tab should reach @key")
		if focused := page.MustEval(`() => document.activeElement && document.activeElement.textContent.trim() === "press"`).Bool(); !focused {
			t.Errorf("a Tab the window stopped moved the focus to %s", page.MustEval(`() => document.activeElement && document.activeElement.outerHTML`).String())
		}
	})
}
