package fyne

import "testing"

// reactiveStyleSrc styles a page node and a runtime instance's node from
// state, beside a field the build can read.
const reactiveStyleSrc = `
import . "sngl:ui"

component row(label string) node {
    var on = false

    button(text=label, style={fontStyle = on ? italic : normal}, @click { on = !on })
}

window {
    var active = 0

    button(text="flip", @click { active = 1 })
    text(value="styled", style={
        color = active == 0 ? #ffffff : #333333,
        fontSize = 1px * (active * 10 + 12),
        fontStyle = italic,
    })
    vbox {
        for var it = ["r"] {
            row(label=it)
        }
    }
}
`

// styleDriverHelpers read the text Fyne actually draws, through the cached
// renderers a window paints from, before and after each click. Reading the
// override's theme instead would pass for a theme nobody refreshed.
const styleDriverHelpers = `package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func walk(o fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	visit(o)
	switch x := o.(type) {
	case *fyne.Container:
		for _, c := range x.Objects {
			walk(c, visit)
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(x).Objects() {
			walk(c, visit)
		}
	}
}

func drawn(t *testing.T, root fyne.CanvasObject, s string) *canvas.Text {
	t.Helper()
	var found *canvas.Text
	walk(root, func(o fyne.CanvasObject) {
		if tx, ok := o.(*canvas.Text); ok && tx.Text == s {
			found = tx
		}
	})
	if found == nil {
		t.Fatalf("no drawn text %q", s)
	}
	return found
}

func tap(t *testing.T, root fyne.CanvasObject, label string) {
	t.Helper()
	var b *widget.Button
	walk(root, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Button); ok && x.Text == label {
			b = x
		}
	})
	if b == nil {
		t.Fatalf("no button %q", label)
	}
	b.OnTapped()
}

// face is the font the painter draws a text in: its own style, as the theme
// in force for it answers.
func face(tx *canvas.Text) fyne.Resource {
	return theme.CurrentForWidget(tx).Font(tx.TextStyle)
}

func rgba(c color.Color) color.NRGBA {
	r, g, b, a := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}
`

const reactiveStyleDriver = styleDriverHelpers + `
func TestReactiveStyle(t *testing.T) {
	test.NewApp()
	m := New()
	w := test.NewWindow(m.BuildUI())
	defer w.Close()
	root := w.Content()

	txt := drawn(t, root, "styled")
	if got, want := rgba(txt.Color), (color.NRGBA{255, 255, 255, 255}); got != want {
		t.Errorf("initial color %v, want %v", got, want)
	}
	if txt.TextSize != 12 {
		t.Errorf("initial size %v, want 12", txt.TextSize)
	}
	if face(txt) != theme.DefaultTheme().Font(fyne.TextStyle{Italic: true}) {
		t.Error("the static italic was not applied")
	}
	// A button draws its label bold, so the row's faces keep that axis.
	row := drawn(t, root, "r").TextStyle
	if face(drawn(t, root, "r")) != theme.DefaultTheme().Font(row) {
		t.Error("the row starts slanted")
	}

	tap(t, root, "flip")
	txt = drawn(t, root, "styled")
	if got, want := rgba(txt.Color), (color.NRGBA{51, 51, 51, 255}); got != want {
		t.Errorf("color after the click %v, want %v", got, want)
	}
	if txt.TextSize != 22 {
		t.Errorf("size after the click %v, want 22", txt.TextSize)
	}
	if face(txt) != theme.DefaultTheme().Font(fyne.TextStyle{Italic: true}) {
		t.Error("the static italic was lost on the rebuild")
	}

	tap(t, root, "r")
	if face(drawn(t, root, "r")) != theme.DefaultTheme().Font(fyne.TextStyle{Bold: row.Bold, Italic: true}) {
		t.Error("the row's slant did not follow its state")
	}
}
`

// TestReactiveStyleRuns builds a theme where the program runs for a style
// field that reads state, and replaces it when that state changes.
func TestReactiveStyleRuns(t *testing.T) {
	model := generateFyneModelBuilt(t, reactiveStyleSrc)
	runEmitted(t, "fyne-reactive-style-", []byte(model), reactiveStyleDriver)
}

// styleValueSrc hands nodes a whole Style held in state: a var replaced by one
// handler and written a field at a time by another, a ternary between two
// literals, and the var again in each copy of a reactive loop.
const styleValueSrc = `
import . "sngl:ui"

window {
    var s Style = {color=#ff0000, fontSize=14px}
    var on = false
    var rows = ["r1"]

    button(text="swap", @click {
        s = {color=#0000ff, fontSize=20px, fontWeight=bold}
        on = !on
    })
    button(text="tint", @click {
        s.color = #00ff00
        rows = ["r1", "r2"]
    })
    text(value="styled", style=s)
    text(value="picked", style=on ? {color=#00ffff} : {color=#ff00ff})
    vbox {
        for var r = rows {
            text(value=r, style=s)
        }
    }
}
`

const styleValueDriver = styleDriverHelpers + `
func count(root fyne.CanvasObject, s string) int {
	n := 0
	walk(root, func(o fyne.CanvasObject) {
		if tx, ok := o.(*canvas.Text); ok && tx.Text == s {
			n++
		}
	})
	return n
}

func TestStyleValue(t *testing.T) {
	test.NewApp()
	m := New()
	w := test.NewWindow(m.BuildUI())
	defer w.Close()
	root := w.Content()

	check := func(when, label string, want color.NRGBA, size float32, bold bool) {
		t.Helper()
		txt := drawn(t, root, label)
		if got := rgba(txt.Color); got != want {
			t.Errorf("%s: %s color %v, want %v", when, label, got, want)
		}
		if txt.TextSize != size {
			t.Errorf("%s: %s size %v, want %v", when, label, txt.TextSize, size)
		}
		if face(txt) != theme.DefaultTheme().Font(fyne.TextStyle{Bold: bold}) {
			t.Errorf("%s: %s bold is not %v", when, label, bold)
		}
	}
	check("initially", "styled", color.NRGBA{255, 0, 0, 255}, 14, false)
	check("initially", "picked", color.NRGBA{255, 0, 255, 255}, 14, false)
	check("initially", "r1", color.NRGBA{255, 0, 0, 255}, 14, false)
	tap(t, root, "swap")
	check("after the swap", "styled", color.NRGBA{0, 0, 255, 255}, 20, true)
	check("after the swap", "picked", color.NRGBA{0, 255, 255, 255}, 14, false)
	check("after the swap", "r1", color.NRGBA{0, 0, 255, 255}, 20, true)
	tap(t, root, "tint")
	check("after the tint", "styled", color.NRGBA{0, 255, 0, 255}, 20, true)
	check("after the tint", "r2", color.NRGBA{0, 255, 0, 255}, 20, true)
	if n := count(root, "r1"); n != 1 {
		t.Errorf("after the loop re-rendered, r1 is drawn %d times, want 1", n)
	}
}
`

// TestStyleValueRuns applies a Style value held in state, and applies it again
// when a handler replaces it or writes one of its fields.
func TestStyleValueRuns(t *testing.T) {
	model := generateFyneModelBuilt(t, styleValueSrc)
	runEmitted(t, "fyne-style-value-", []byte(model), styleValueDriver)
}

// slotThemeSrc's loop renders into a vbox its style colors red, a row more
// once the window is up.
const slotThemeSrc = `
import ui "sngl:ui"

ui.window {
    var items = ["x"]

    ui.vbox(style={color=#ff0000}) {
        ui.text(value="static")
        for var it = items {
            ui.text(value=it)
        }
    }
    ui.button(text="more", @click { items.push("y") })
}
`

const slotThemeDriver = styleDriverHelpers + `
func TestARowARenderSlotAddsTakesTheBoxsColor(t *testing.T) {
	test.NewApp()
	m := New()
	w := test.NewWindow(m.BuildUI())
	defer w.Close()
	root := w.Content()
	red := color.NRGBA{255, 0, 0, 255}
	if got := rgba(drawn(t, root, "static").Color); got != red {
		t.Errorf("the static row is drawn %v, want %v", got, red)
	}
	if got := rgba(drawn(t, root, "x").Color); got != red {
		t.Fatalf("the first row is drawn %v, want %v", got, red)
	}
	tap(t, root, "more")
	if got := rgba(drawn(t, root, "y").Color); got != red {
		t.Errorf("the row the click added is drawn %v, want its box's %v", got, red)
	}
}
`

// A row a render slot adds under a styled box is drawn in the box's theme,
// where it kept the one it had until the box refreshed.
func TestARenderSlotRowTakesItsBoxsThemeRuns(t *testing.T) {
	runEmitted(t, "fyne-slot-theme-", []byte(generateFyneModelBuilt(t, slotThemeSrc)), slotThemeDriver)
}
