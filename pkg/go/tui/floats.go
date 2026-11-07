package tui

import (
	"slices"

	"charm.land/lipgloss/v2"
)

// Floats is what a view draws over itself where a widget is open: the
// popover a select lists its choices in, below the select. The program's
// Model holds one, which its widgets are handed with each view and update.
//
// A widget drawn open marks its block and hands over what floats below it
// (Float); the view takes the marks out where it draws them (Scan), and once
// it is whole Draw lays each float under the block marked for it -- above it
// where there is no room below -- and keeps where it went, which a click is
// asked against (At). Marks are zero-width, as a click's are
// (Clicks), and of a kind of their own, so a block both pressable and open
// carries both.
//
// While one is open the program's Update closes it on Esc, on Tab and on a
// click outside it, and a click inside it is the widget's rather than a
// press of whatever it covers.
type Floats struct {
	marks Clicks[pendingFloat]
	drawn []drawnFloat
	// opened is every select opened since the Floats was made, which Close
	// closes and Open asks.
	opened []*selectState
}

type pendingFloat struct {
	owner *selectState
	box   string
}

type drawnFloat struct {
	owner          *selectState
	x0, y0, x1, y1 int // inclusive
}

// NewFloats is a Floats with nothing open.
func NewFloats() *Floats {
	return &Floats{marks: Clicks[pendingFloat]{opens: '|', closes: '}'}}
}

// Float is block marked as owner's, with box to draw below it.
func (f *Floats) Float(owner *selectState, block, box string) string {
	return f.marks.Mark(pendingFloat{owner: owner, box: box}, block)
}

func (f *Floats) track(owner *selectState) {
	if slices.Contains(f.opened, owner) {
		return
	}
	f.opened = append(f.opened, owner)
}

// Open reports whether a widget's float is open.
func (f *Floats) Open() bool {
	for _, o := range f.opened {
		if o.open {
			return true
		}
	}
	return false
}

// Close closes every open float.
func (f *Floats) Close() {
	for _, o := range f.opened {
		o.open = false
	}
	f.opened = f.opened[:0]
	f.drawn = f.drawn[:0]
}

// At reports whether a click at column x, row y lands on a float drawn by
// the last view.
func (f *Floats) At(x, y int) bool {
	for _, d := range f.drawn {
		if x >= d.x0 && x <= d.x1 && y >= d.y0 && y <= d.y1 {
			return true
		}
	}
	return false
}

// rowAt is the row of owner's float a click at x, y lands on, counted from
// the first drawn inside its border.
func (f *Floats) rowAt(owner *selectState, x, y int) (int, bool) {
	for _, d := range f.drawn {
		if d.owner == owner && x > d.x0 && x < d.x1 && y > d.y0 && y < d.y1 {
			return owner.first + y - d.y0 - 1, true
		}
	}
	return 0, false
}

// Scan is s without its marks, keeping where each marked block is with s's
// top left corner at column x, row y: the view's, and each overlay's box
// where it lands, as Clicks.Scan takes them.
func (f *Floats) Scan(s string, x, y int) string { return f.marks.Scan(s, x, y) }

// Draw is view, w×h cells of a terminal, with the float of every block Scan
// found laid on it.
func (f *Floats) Draw(view string, w, h int) string {
	f.drawn = f.drawn[:0]
	boxes, marked := f.marks.boxes, f.marks.marked
	f.marks.Reset()
	for _, b := range boxes {
		p := marked[b.i]
		if !p.owner.open {
			continue
		}
		cw, ch := overlayDims(view, w, h)
		bw, bh := lipgloss.Width(p.box), lipgloss.Height(p.box)
		x, y := min(b.x0, max(cw-bw, 0)), b.y1+1
		if y+bh > ch && b.y0-bh >= 0 {
			y = b.y0 - bh
		}
		view = composite(view, p.box, x, y, cw, ch)
		f.drawn = append(f.drawn, drawnFloat{owner: p.owner, x0: x, y0: y, x1: x + bw - 1, y1: y + bh - 1})
	}
	return view
}
