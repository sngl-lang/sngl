package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
)

// SetGridColumns lays c's children out n to a row, as a container
// container.NewGridWithColumns(n) built does. The count is that constructor's
// argument and fyne's Container has no setter for it, so a grid whose count is
// read from state -- a table's columns -- kept the count it was built with.
func SetGridColumns(c *fyne.Container, n int) {
	c.Layout = layout.NewGridLayoutWithColumns(n)
	c.Refresh()
}

// Grid is sngl:ui's grid. With Columns it lays its children that many to a
// row, in columns of equal width sharing the grid's, each row as tall as its
// tallest child. With none it flows them a row at a time at their own
// minimum width, starting a row where the next would not fit, as a CSS row
// that wraps does.
//
// How many rows a flow takes is its width's to say, and Fyne asks a layout's
// minimum before it gives one a width. A flow's minimum is as wide as its
// widest child and as tall as the rows the width it was last laid out at
// takes; where laying it out finds a different height, it refreshes, so the
// box it is in asks again.
type Grid struct {
	// Columns is the count of a grid of columns; zero flows.
	Columns int
	// Gap is the space between two rows and between two children of one;
	// zero is the theme's padding.
	Gap   float32
	c     *fyne.Container
	width float32
	tall  float32
}

// NewGrid is a grid of columns children to a row, or a flow where columns is
// zero.
func NewGrid(columns int, objects ...fyne.CanvasObject) *fyne.Container {
	l := &Grid{Columns: columns}
	l.c = container.New(l, objects...)
	return l.c
}

// SetColumns is a Grid's column count, which a grid's prop may change.
func SetColumns(c *fyne.Container, n int) {
	if l, ok := c.Layout.(*Grid); ok && l.Columns != n {
		l.Columns = n
		c.Refresh()
	}
}

func (l *Grid) gap() float32 {
	if l.Gap > 0 {
		return l.Gap
	}
	return theme.Padding()
}

func shownOf(objects []fyne.CanvasObject) []fyne.CanvasObject {
	var shown []fyne.CanvasObject
	for _, o := range objects {
		if o.Visible() {
			shown = append(shown, o)
		}
	}
	return shown
}

// rows cuts shown into the rows a width holds, each the indexes it runs to.
func (l *Grid) rows(shown []fyne.CanvasObject, width float32) []int {
	var ends []int
	if l.Columns > 0 {
		for i := l.Columns; i < len(shown); i += l.Columns {
			ends = append(ends, i)
		}
		return append(ends, len(shown))
	}
	gap := l.gap()
	run := float32(0)
	for i, o := range shown {
		w := o.MinSize().Width
		if i > 0 && run > 0 && run+gap+w > width {
			ends = append(ends, i)
			run = 0
		}
		if run > 0 {
			run += gap
		}
		run += w
	}
	return append(ends, len(shown))
}

// height is the rows' heights, and their sum with the gaps between them.
func (l *Grid) height(shown []fyne.CanvasObject, ends []int) ([]float32, float32) {
	heights := make([]float32, len(ends))
	total := float32(0)
	start := 0
	for r, end := range ends {
		for _, o := range shown[start:end] {
			heights[r] = max(heights[r], o.MinSize().Height)
		}
		total += heights[r]
		start = end
	}
	if len(ends) > 1 {
		total += l.gap() * float32(len(ends)-1)
	}
	return heights, total
}

func (l *Grid) MinSize(objects []fyne.CanvasObject) fyne.Size {
	shown := shownOf(objects)
	if len(shown) == 0 {
		return fyne.NewSize(0, 0)
	}
	widest := float32(0)
	for _, o := range shown {
		widest = max(widest, o.MinSize().Width)
	}
	width := widest
	if l.Columns > 0 {
		n := min(l.Columns, len(shown))
		width = widest*float32(n) + l.gap()*float32(n-1)
	}
	_, tall := l.height(shown, l.rows(shown, max(l.width, width)))
	l.tall = tall
	return fyne.NewSize(width, tall)
}

func (l *Grid) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	shown := shownOf(objects)
	l.width = size.Width
	if len(shown) == 0 {
		return
	}
	gap := l.gap()
	ends := l.rows(shown, size.Width)
	heights, tall := l.height(shown, ends)
	column := float32(0)
	if l.Columns > 0 {
		column = max((size.Width-gap*float32(l.Columns-1))/float32(l.Columns), 0)
	}
	y, start := float32(0), 0
	for r, end := range ends {
		x := float32(0)
		for _, o := range shown[start:end] {
			w := column
			if l.Columns == 0 {
				w = min(o.MinSize().Width, size.Width)
			}
			o.Move(fyne.NewPos(x, y))
			o.Resize(fyne.NewSize(w, heights[r]))
			x += w + gap
		}
		y += heights[r] + gap
		start = end
	}
	if tall != l.tall && l.c != nil {
		l.tall = tall
		l.c.Refresh()
	}
}
