// Package fynelayout is the fyne.Layout behind SNGL's vbox and hbox, the Box
// a styled node is shown in, and the anchor a render slot keeps its place in
// its container by.
//
// Fyne's own boxes pack children at their minimum size, so a child asking for
// a share of the box has nowhere to say it. Weighted distributes what the box
// has left over after its fixed children in proportion to what each child's
// Box asks for, which is what `flex` means everywhere else in SNGL.
package fynelayout

import (
	"reflect"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
)

// Weighted lays children along one axis. A child that is not a Box with a
// Grow keeps its minimum size on that axis; the rest share what is left in
// proportion to theirs.
type Weighted struct {
	Horizontal bool
	// Gap is the space between two children; zero is the theme's padding.
	Gap     float32
	Cross   int
	Justify int
	c       *fyne.Container
}

func NewVBox(objects ...fyne.CanvasObject) *fyne.Container {
	return newBox(&Weighted{}, objects)
}

func NewHBox(objects ...fyne.CanvasObject) *fyne.Container {
	return newBox(&Weighted{Horizontal: true}, objects)
}

func newBox(l *Weighted, objects []fyne.CanvasObject) *fyne.Container {
	l.c = container.New(l, objects...)
	return l.c
}

// grow is the share o takes: its Box's, or one for a spacer that expands
// along this axis, as Fyne's own boxes give it.
func (l *Weighted) grow(o fyne.CanvasObject) float32 {
	if b, ok := o.(*Box); ok {
		if g := b.Grow(); g > 0 {
			return g
		}
		o = b.Content
	}
	if s, ok := o.(layout.SpacerObject); ok && (l.Horizontal && s.ExpandHorizontal() || !l.Horizontal && s.ExpandVertical()) {
		return 1
	}
	return 0
}

func (l *Weighted) gap() float32 {
	if l.Gap > 0 {
		return l.Gap
	}
	return theme.Padding()
}

func (l *Weighted) axes(s fyne.Size) (main, cross float32) {
	if l.Horizontal {
		return s.Width, s.Height
	}
	return s.Height, s.Width
}

func (l *Weighted) size(main, cross float32) fyne.Size {
	if l.Horizontal {
		return fyne.NewSize(main, cross)
	}
	return fyne.NewSize(cross, main)
}

func (l *Weighted) pos(main, cross float32) fyne.Position {
	if l.Horizontal {
		return fyne.NewPos(main, cross)
	}
	return fyne.NewPos(cross, main)
}

// MinSize is what the box needs with every child at its own minimum -- a
// growing child included, since a share of the surplus says nothing about the
// floor.
func (l *Weighted) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var main, cross float32
	visible := 0
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		omain, ocross := l.axes(o.MinSize())
		main += omain
		cross = max(cross, ocross)
		visible++
	}
	if visible > 1 {
		main += l.gap() * float32(visible-1)
	}
	return l.size(main, cross)
}

func (l *Weighted) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	main, cross := l.axes(size)

	var shown []fyne.CanvasObject
	for _, o := range objects {
		if o.Visible() {
			shown = append(shown, o)
		}
	}
	if len(shown) == 0 {
		return
	}
	extent := make([]float32, len(shown))
	grows := make([]float32, len(shown))
	var fixed, totalGrow float32
	growing := 0
	for i, o := range shown {
		if b, ok := o.(*Box); ok {
			b.in = l
		}
		omain, _ := l.axes(o.MinSize())
		extent[i] = omain
		if g := l.grow(o); g > 0 {
			grows[i] = g
			totalGrow += g
			growing++
		} else {
			fixed += omain
		}
	}
	gap := l.gap()
	free := max(main-fixed-gap*float32(len(shown)-1), 0)
	// A share below a child's own minimum is the minimum, as CSS clamps a
	// flex item, and what is left is shared again among the rest.
	for clamped := true; clamped && growing > 0; {
		clamped = false
		for i, g := range grows {
			if g > 0 && free*g/totalGrow < extent[i] {
				free, totalGrow = max(free-extent[i], 0), totalGrow-g
				grows[i] = 0
				growing--
				clamped = true
			}
		}
	}
	for i, g := range grows {
		if g > 0 {
			extent[i] = free * g / totalGrow
		}
	}

	used := gap * float32(len(shown)-1)
	for _, e := range extent {
		used += e
	}
	slack := max(main-used, 0)
	offset, between := float32(0), gap
	if growing == 0 {
		n := float32(len(shown))
		switch l.Justify {
		case JustifyCenter:
			offset = slack / 2
		case JustifyEnd:
			offset = slack
		case JustifyBetween:
			if len(shown) > 1 {
				between += slack / (n - 1)
			}
		case JustifyAround:
			offset = slack / n / 2
			between += slack / n
		case JustifyEvenly:
			offset = slack / (n + 1)
			between += offset
		}
	}

	for i, o := range shown {
		_, ocross := l.axes(o.MinSize())
		at := float32(0)
		switch l.Cross {
		case CrossStart:
		case CrossCenter:
			at = (cross - ocross) / 2
		case CrossEnd:
			at = cross - ocross
		default:
			ocross = cross
		}
		o.Move(l.pos(offset, at))
		o.Resize(l.size(extent[i], ocross))
		offset += extent[i] + between
	}
}

// Adopt makes c a Weighted box when o asks it for a share and c is one of
// Fyne's own boxes, which would pack o at its minimum: a box the generated
// code built as Fyne's may still be handed a growing child -- a page a
// navigation stack shows, a slot's content.
func Adopt(c *fyne.Container, o fyne.CanvasObject) {
	if c == nil {
		return
	}
	if b, ok := o.(*Box); !ok || b.Grow() == 0 {
		return
	}
	switch reflect.TypeOf(c.Layout) {
	case vboxType:
		c.Layout = &Weighted{c: c}
	case hboxType:
		c.Layout = &Weighted{Horizontal: true, c: c}
	}
}

var (
	vboxType = reflect.TypeOf(layout.NewVBoxLayout())
	hboxType = reflect.TypeOf(layout.NewHBoxLayout())
)
