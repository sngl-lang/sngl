package fynelayout_test

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/fynelayout"
)

// cursorOver is the cursor a desktop driver shows over o, every object of
// whose tree is under the pointer: the glfw window walks the visible tree,
// parents before their children, and the last desktop.Cursorable it meets
// says.
func cursorOver(o fyne.CanvasObject) desktop.Cursor {
	cursor := desktop.Cursor(desktop.DefaultCursor)
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if !o.Visible() {
			return
		}
		if c, ok := o.(desktop.Cursorable); ok {
			cursor = c.Cursor()
		}
		switch o := o.(type) {
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		case fyne.Widget:
			for _, child := range fynetest.WidgetRenderer(o).Objects() {
				walk(child)
			}
		}
	}
	walk(o)
	return cursor
}

func cursored(content fyne.CanvasObject, cursor int) *fynelayout.Box {
	b := fynelayout.NewBox(content)
	b.SetLook(fynelayout.Look{Cursor: cursor})
	return b
}

// A Look's cursor is shown over its box, over the widget's own: an entry's
// text cursor gives way to the pointer its Style asks for, as CSS's does. A
// box inside another says over the one around it, and a box asking for none
// leaves the one around it standing.
func TestBoxShowsItsLooksCursor(t *testing.T) {
	if got := cursorOver(cursored(widget.NewLabel("a"), fynelayout.CursorPointer)); got != desktop.PointerCursor {
		t.Errorf("a label's box shows %v, want the pointer", got)
	}
	if got := cursorOver(cursored(widget.NewEntry(), fynelayout.CursorPointer)); got != desktop.PointerCursor {
		t.Errorf("an entry's box shows %v, want the pointer over the entry's text cursor", got)
	}
	inner := cursored(widget.NewLabel("b"), fynelayout.CursorCrosshair)
	if got := cursorOver(cursored(container.NewVBox(inner), fynelayout.CursorPointer)); got != desktop.CrosshairCursor {
		t.Errorf("a box inside a pointer's shows %v, want its own crosshair", got)
	}
	plain := cursored(widget.NewLabel("c"), 0)
	if got := cursorOver(cursored(container.NewVBox(plain), fynelayout.CursorPointer)); got != desktop.PointerCursor {
		t.Errorf("a box asking for no cursor inside a pointer's shows %v, want the pointer", got)
	}
	if got := cursorOver(cursored(widget.NewLabel("d"), 0)); got != desktop.DefaultCursor {
		t.Errorf("a box asking for no cursor shows %v, want the default", got)
	}
}
