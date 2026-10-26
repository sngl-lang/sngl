package fynelayout

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// SlotAnchor returns the anchor a render slot inserts its entries before: a,
// when c holds it, or a new hidden object added at the end of c.
//
// The slot's first render runs while its container is being built, at the
// position the slot was written, so the end of c is where the anchor belongs.
// A container built again holds none of the old one's objects, and gets a new
// anchor the same way.
func SlotAnchor(c *fyne.Container, a fyne.CanvasObject) fyne.CanvasObject {
	// The application's children are in no order a window shows.
	if c == App {
		return nil
	}
	if a != nil && slices.Contains(c.Objects, a) {
		return a
	}
	r := canvas.NewRectangle(color.Transparent)
	r.Hide()
	c.Add(r)
	return r
}

// Nothing is what an instance whose body renders no node -- a request, an
// effect -- is in the box holding it: hidden, so the box gives it no room and
// no gap.
func Nothing() fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.Hide()
	return r
}

// SlotBox is the box a render slot in a single-child container renders into:
// b, or a new one.
func SlotBox(b *fyne.Container) *fyne.Container {
	if b == nil {
		b = NewVBox()
	}
	return b
}

// InsertBefore puts o into c immediately before anchor, or at the end of c
// when c does not hold anchor.
func InsertBefore(c *fyne.Container, anchor, o fyne.CanvasObject) {
	if c == App {
		AppAttach(o)
		return
	}
	Adopt(c, o)
	defer menuMoved(c, o)
	i := slices.Index(c.Objects, anchor)
	if i < 0 {
		c.Add(o)
		return
	}
	c.Objects = slices.Insert(c.Objects, i, o)
	relayout(c)
}

// relayout lays c out again and asks for it to be drawn, as Container.Add
// does, without refreshing what c already held. A refresh reaches every child,
// and a theme override refreshed is a new theme scope for Fyne's text cache,
// so every label under one was measured again: a table a render slot filled
// row by row shaped all its text again for each row.
func relayout(c *fyne.Container) {
	if c.Layout != nil {
		c.Layout.Layout(c.Objects, c.Size())
	}
	canvas.Refresh(c)
}
