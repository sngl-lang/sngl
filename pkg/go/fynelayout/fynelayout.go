// Package fynelayout is the fyne.Layout behind SNGL's `style.flex`, and the
// anchor a render slot keeps its place in its container by.
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

import (
	"reflect"
	"runtime"
	"sync"
	"unsafe"
	"weak"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
)

// Weighted lays children along one axis. A child whose weight is zero keeps
// its minimum size on that axis; the rest share what is left in proportion to
// theirs. On the cross axis every child fills the box.
//
// A child's weight and margin are its own, set by Share where it is built,
// rather than a position in the box: a render slot inserts its content and
// its anchor among the children the box was built with, so the i'th object
// Layout is handed is not the i'th child the source wrote.
type Weighted struct {
	Horizontal bool
	// Gap is the space between two children, Padding the inset around all of
	// them.
	Gap     float32
	Padding float32
}

// New returns the layout as the interface, which is what the generated
// `container.New(...)` call takes.
func New(horizontal bool, gap, padding float32) fyne.Layout {
	return Weighted{Horizontal: horizontal, Gap: gap, Padding: padding}
}

// share is what one child asked of the box holding it.
type share struct {
	weight, margin float32
	// self is the child, held weakly: the address is the key, and a key
	// outliving its object must not hand its share to whatever is allocated
	// there next.
	self weak.Pointer[byte]
}

var (
	sharesMu sync.Mutex
	shares   = map[uintptr]share{}
)

// address is where o's object starts, or nil where o is not a pointer.
func address(o fyne.CanvasObject) unsafe.Pointer {
	v := reflect.ValueOf(o)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}
	return v.UnsafePointer()
}

// Share records that o asks the box it is in for weight shares of its spare
// room, inset by margin on every side. It is said where o is built, which is
// before -- and wherever -- o is put in a box. A box that packs at minimum
// size is made a Weighted one when a child with a weight arrives in it. It
// returns o, so a wrapper built where it is appended can say it in place.
func Share(o fyne.CanvasObject, weight, margin float32) fyne.CanvasObject {
	p := address(o)
	if p == nil {
		return o
	}
	key := uintptr(p)
	sharesMu.Lock()
	defer sharesMu.Unlock()
	old, had := shares[key]
	if weight == 0 && margin == 0 {
		delete(shares, key)
		return o
	}
	shares[key] = share{weight: weight, margin: margin, self: weak.Make((*byte)(p))}
	if !had || old.self.Value() != (*byte)(p) {
		runtime.AddCleanup((*byte)(p), forget, key)
	}
	return o
}

// forget drops the share of an object the collector has taken, unless the
// address already holds a newer one's.
func forget(key uintptr) {
	sharesMu.Lock()
	defer sharesMu.Unlock()
	if s, ok := shares[key]; ok && s.self.Value() == nil {
		delete(shares, key)
	}
}

// shareOf is what o asked for, or nothing.
func shareOf(o fyne.CanvasObject) (weight, margin float32) {
	p := address(o)
	if p == nil {
		return 0, 0
	}
	sharesMu.Lock()
	s, ok := shares[uintptr(p)]
	sharesMu.Unlock()
	if !ok || s.self.Value() != (*byte)(p) {
		return 0, 0
	}
	return s.weight, s.margin
}

// Adopt makes c lay o out by its share when o has one and c is one of Fyne's
// own boxes, which would pack it at its minimum: a box the source wrote with
// no flexing child of its own may still be handed one -- a page a navigation
// stack shows, a slot's content -- and the generated code built it as Fyne's
// box. The gap Fyne's box puts between children is kept.
func Adopt(c *fyne.Container, o fyne.CanvasObject) {
	if c == nil {
		return
	}
	if w, _ := shareOf(o); w == 0 {
		return
	}
	switch reflect.TypeOf(c.Layout) {
	case vboxType:
		c.Layout = New(false, theme.Padding(), 0)
	case hboxType:
		c.Layout = New(true, theme.Padding(), 0)
	}
}

var (
	vboxType = reflect.TypeOf(layout.NewVBoxLayout())
	hboxType = reflect.TypeOf(layout.NewHBoxLayout())
)

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
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		_, margin := shareOf(o)
		m := margin * 2
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
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		visible++
		w, margin := shareOf(o)
		m := margin * 2
		if w > 0 {
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
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		w, margin := shareOf(o)
		slot := float32(0)
		if w > 0 && totalWeight > 0 {
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
