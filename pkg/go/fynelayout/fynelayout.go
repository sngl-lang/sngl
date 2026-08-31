// Package fynelayout is the fyne.Layout behind SNGL's `style.flex`.
//
// Fyne's own boxes pack children at their minimum size, so a child asking for
// a share of the box has nowhere to say it. Weighted distributes what the box
// has left over after its fixed children in proportion to the weights, which
// is what `flex` means everywhere else in SNGL.
//
// The generated code constructs one per container, so the padding and gap the
// container's own style asked for, and each child's margin, are carried here
// too: they are the same measurement, and inset by a separate wrapper widget
// they would each cost a node in the tree.
package fynelayout

import "fyne.io/fyne/v2"

// Weighted lays children along one axis. A child whose weight is zero keeps
// its minimum size on that axis; the rest share what is left in proportion to
// theirs. On the cross axis every child fills the box.
//
// Weights and Margins are positional -- index i is the i'th object Layout is
// handed -- and either may be short, which reads as zero.
type Weighted struct {
	Horizontal bool
	Weights    []float32
	Margins    []float32
	// Gap is the space between two children, Padding the inset around all of
	// them.
	Gap     float32
	Padding float32
}

// New returns the layout as the interface, which is what the generated
// `container.New(...)` call takes.
func New(horizontal bool, weights, margins []float32, gap, padding float32) fyne.Layout {
	return Weighted{Horizontal: horizontal, Weights: weights, Margins: margins, Gap: gap, Padding: padding}
}

func at(vals []float32, i int) float32 {
	if i < 0 || i >= len(vals) {
		return 0
	}
	return vals[i]
}

func (l Weighted) axes(s fyne.Size) (main, cross float32) {
	if l.Horizontal {
		return s.Width, s.Height
	}
	return s.Height, s.Width
}

func (l Weighted) size(main, cross float32) fyne.Size {
	if l.Horizontal {
		return fyne.NewSize(main, cross)
	}
	return fyne.NewSize(cross, main)
}

func (l Weighted) pos(main, cross float32) fyne.Position {
	if l.Horizontal {
		return fyne.NewPos(main, cross)
	}
	return fyne.NewPos(cross, main)
}

// MinSize is what the box needs with every child at its own minimum -- a
// weighted child included, since a weight asks for a share of the surplus and
// says nothing about the floor.
func (l Weighted) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var main, cross float32
	visible := 0
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		m := at(l.Margins, i) * 2
		omain, ocross := l.axes(o.MinSize())
		main += omain + m
		if ocross+m > cross {
			cross = ocross + m
		}
		visible++
	}
	if visible > 1 {
		main += l.Gap * float32(visible-1)
	}
	return l.size(main+l.Padding*2, cross+l.Padding*2)
}

func (l Weighted) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	main, cross := l.axes(size)

	visible := 0
	var fixed, totalWeight float32
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		visible++
		m := at(l.Margins, i) * 2
		if w := at(l.Weights, i); w > 0 {
			totalWeight += w
			fixed += m
			continue
		}
		omain, _ := l.axes(o.MinSize())
		fixed += omain + m
	}

	avail := main - l.Padding*2
	if visible > 1 {
		avail -= l.Gap * float32(visible-1)
	}
	free := avail - fixed
	if free < 0 {
		free = 0
	}

	offset := l.Padding
	for i, o := range objects {
		if !o.Visible() {
			continue
		}
		margin := at(l.Margins, i)
		slot := float32(0)
		if w := at(l.Weights, i); w > 0 && totalWeight > 0 {
			slot = free * w / totalWeight
		} else {
			slot, _ = l.axes(o.MinSize())
		}
		ocross := cross - l.Padding*2 - margin*2
		if ocross < 0 {
			ocross = 0
		}
		o.Move(l.pos(offset+margin, l.Padding+margin))
		o.Resize(l.size(slot, ocross))
		offset += slot + margin*2 + l.Gap
	}
}
