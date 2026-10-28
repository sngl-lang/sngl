package fynelayout

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
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
	takeTheme(c, o)
	i := slices.Index(c.Objects, anchor)
	if i < 0 {
		c.Add(o)
		return
	}
	c.Objects = slices.Insert(c.Objects, i, o)
	relayout(c)
}

// takeTheme puts o under the theme override c's other objects are under.
// Fyne's ThemeOverride reaches what is added beneath it only when refreshed,
// and that would refresh all of c.
func takeTheme(c *fyne.Container, o fyne.CanvasObject) {
	app := theme.Current()
	for _, s := range c.Objects {
		if s == o || !takesScope(s) {
			continue
		}
		if th := theme.CurrentForWidget(s); !sameAny(th, app) {
			container.NewThemeOverride(o, th)
			return
		}
	}
}

// takesScope reports whether Fyne records the override s is under on s
// itself: it records none on a container, and a nested override is a scope
// of its own.
func takesScope(s fyne.CanvasObject) bool {
	switch s.(type) {
	case *fyne.Container, *container.ThemeOverride:
		return false
	}
	return true
}

func sameAny(a, b any) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
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
