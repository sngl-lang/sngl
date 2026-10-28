package fynelayout

import (
	"image/color"
	"slices"
	"sync"

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
	themeLater.Lock()
	themeLater.inserted = append(themeLater.inserted, insertion{c, o})
	themeLater.Unlock()
	i := slices.Index(c.Objects, anchor)
	if i < 0 {
		c.Add(o)
		return
	}
	c.Objects = slices.Insert(c.Objects, i, o)
	relayout(c)
}

// themeLater is what InsertBefore put into a container, to be put under the
// theme override the objects beside it are under before the next draw.
// Fyne's ThemeOverride reaches what is added beneath it only when refreshed,
// which would refresh all of the container; and applying one clears Fyne's
// text-metrics cache, so applied on each insert, every row a slot inserted
// measured its siblings again. Applied together, a render's rows cost one
// measuring.
var themeLater struct {
	sync.Mutex
	inserted []insertion
}

type insertion struct {
	c *fyne.Container
	o fyne.CanvasObject
}

// applyThemes puts what InsertBefore inserted since it last ran under the
// override beside it, and lays out again each container one was given to. A
// Box runs it as a draw walks it, and not as it is measured or laid out: a
// boxed cell of a row a render builds is measured before the row is inserted,
// and would apply the rows inserted before it one by one.
func applyThemes() {
	themeLater.Lock()
	inserted := themeLater.inserted
	themeLater.inserted = nil
	themeLater.Unlock()
	if len(inserted) == 0 {
		return
	}
	var order []*fyne.Container
	added := map[*fyne.Container][]fyne.CanvasObject{}
	for _, in := range inserted {
		if !slices.Contains(in.c.Objects, in.o) || slices.Contains(added[in.c], in.o) {
			continue
		}
		if added[in.c] == nil {
			order = append(order, in.c)
		}
		added[in.c] = append(added[in.c], in.o)
	}
	app := theme.Current()
	for _, c := range order {
		th := themeBeside(c, app)
		if th == nil {
			continue
		}
		// One override over all of them, held by a container of no window,
		// measures each once: one apiece measured each again after the next
		// cleared the cache.
		container.NewThemeOverride(container.NewWithoutLayout(added[c]...), th)
		relayout(c)
	}
}

// themeBeside is the override the objects c holds are under, nil where none is.
func themeBeside(c *fyne.Container, app fyne.Theme) fyne.Theme {
	for _, s := range c.Objects {
		if !takesScope(s) {
			continue
		}
		if th := theme.CurrentForWidget(s); !sameAny(th, app) {
			return th
		}
	}
	return nil
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
