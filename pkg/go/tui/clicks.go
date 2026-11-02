package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Clicks is where a view drew what a click can press. A view marks each
// pressable block as it renders it, which wraps the block's text in two
// escape sequences no terminal is sent; once the view is whole, Scan takes
// them out and keeps the box between them, and At answers a click with the
// block under it.
//
// The marks are zero-width to everything that measures a rendered string --
// lipgloss joins and pads around them as it does around a color -- so a
// block is found where its layout put it, however the layout got there.
type Clicks[T any] struct {
	marked []T
	boxes  []clickBox
}

type clickBox struct {
	x0, y0, x1, y1 int // x1 and y1 inclusive
	i              int
}

// Reset forgets what the last view drew; a view calls it before it marks.
func (c *Clicks[T]) Reset() {
	c.marked = c.marked[:0]
	c.boxes = c.boxes[:0]
}

// Mark is s, a block a click on presses v.
func (c *Clicks[T]) Mark(v T, s string) string {
	if s == "" {
		return s
	}
	i := len(c.marked)
	c.marked = append(c.marked, v)
	n := strconv.Itoa(i)
	return "\x1b[" + n + "z" + s + "\x1b[" + n + "{"
}

// Cover forgets every box found so far: what an overlay is drawn over takes
// no click while it is open.
func (c *Clicks[T]) Cover() {
	c.boxes = c.boxes[:0]
}

// Scan is s without its marks, keeping the box each marked block covers, with
// s's top left corner at column x, row y. A block's box runs from its first
// cell to its last, across every row between them, which is the block a
// lipgloss style renders. A mark whose other half was cut away marks nothing.
func (c *Clicks[T]) Scan(s string, x, y int) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	type open struct{ x, y int }
	opened := map[int]open{}
	var out strings.Builder
	out.Grow(len(s))
	col, row := x, y
	var state byte
	for len(s) > 0 {
		seq, width, n, next := ansi.DecodeSequence(s, state, nil)
		state = next
		s = s[n:]
		if final := seq[len(seq)-1]; len(seq) > 3 && seq[0] == ansi.ESC && seq[1] == '[' && (final == 'z' || final == '{') {
			if i, err := strconv.Atoi(seq[2 : len(seq)-1]); err == nil && i >= 0 && i < len(c.marked) {
				if final == 'z' {
					opened[i] = open{col, row}
				} else if o, ok := opened[i]; ok {
					delete(opened, i)
					c.boxes = append(c.boxes, clickBox{x0: o.x, y0: o.y, x1: max(col-1, o.x), y1: row, i: i})
				}
				continue
			}
		}
		out.WriteString(seq)
		if seq == "\n" {
			col, row = x, row+1
			continue
		}
		col += width
	}
	return out.String()
}

// At is what a click at column x, row y presses: the smallest box holding it,
// which is the innermost of blocks drawn one inside another.
func (c *Clicks[T]) At(x, y int) (T, bool) {
	best := -1
	area := 0
	for k, b := range c.boxes {
		if x < b.x0 || x > b.x1 || y < b.y0 || y > b.y1 {
			continue
		}
		if a := (b.x1 - b.x0 + 1) * (b.y1 - b.y0 + 1); best < 0 || a < area {
			best, area = k, a
		}
	}
	if best < 0 {
		var zero T
		return zero, false
	}
	return c.marked[c.boxes[best].i], true
}
