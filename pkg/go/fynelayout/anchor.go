package fynelayout

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// SlotAnchor returns the anchor a render slot inserts its entries before: a,
// when c holds it, or a new hidden object added at the end of c.
//
// The slot's first render runs while its container is being built, at the
// position the slot was written, so the end of c is where the anchor belongs.
// A container built again holds none of the old one's objects, and gets a new
// anchor the same way.
func SlotAnchor(c *fyne.Container, a fyne.CanvasObject) fyne.CanvasObject {
	if a != nil && slices.Contains(c.Objects, a) {
		return a
	}
	r := canvas.NewRectangle(color.Transparent)
	r.Hide()
	c.Add(r)
	return r
}

// SlotBox is the box a render slot in a single-child container renders into:
// b, or a new one.
func SlotBox(b *fyne.Container) *fyne.Container {
	if b == nil {
		b = container.NewVBox()
	}
	return b
}

// InsertBefore puts o into c immediately before anchor, or at the end of c
// when c does not hold anchor.
func InsertBefore(c *fyne.Container, anchor, o fyne.CanvasObject) {
	i := slices.Index(c.Objects, anchor)
	if i < 0 {
		c.Add(o)
		return
	}
	c.Objects = slices.Insert(c.Objects, i, o)
	c.Refresh()
}
