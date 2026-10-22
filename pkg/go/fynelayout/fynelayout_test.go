package fynelayout_test

import (
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"duckfam.us/sngl/pkg/go/fynelayout"
)

// A canvas object refreshes itself when its minimum changes, and that reaches
// for the running app. There is no window in these tests, only geometry.
func TestMain(m *testing.M) {
	fynetest.NewApp()
	os.Exit(m.Run())
}

// box is a child with a known minimum, which is all this layout reads.
func box(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(nil)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

// shared is o asking its box for weight shares, inset by margin.
func shared(o fyne.CanvasObject, weight, margin float32) fyne.CanvasObject {
	fynelayout.Share(o, weight, margin)
	return o
}

func TestWeightsSplitTheSurplus(t *testing.T) {
	a, b := shared(box(10, 10), 1, 0), shared(box(10, 10), 3, 0)
	l := fynelayout.New(true, 0, 0)
	l.Layout([]fyne.CanvasObject{a, b}, fyne.NewSize(400, 50))

	if got := a.Size().Width; got != 100 {
		t.Errorf("weight 1 of 4 got width %v, want 100", got)
	}
	if got := b.Size().Width; got != 300 {
		t.Errorf("weight 3 of 4 got width %v, want 300", got)
	}
	if got := b.Position().X; got != 100 {
		t.Errorf("second child starts at %v, want 100", got)
	}
	// The cross axis is the box: that is what "fill" means.
	if got := a.Size().Height; got != 50 {
		t.Errorf("cross axis got %v, want 50", got)
	}
}

// A weight of zero keeps the child's minimum, and only what is left over is
// shared -- which is what makes a fixed header above a flexing body work.
func TestUnweightedChildrenKeepTheirMinimum(t *testing.T) {
	fixed, grows := box(10, 30), shared(box(10, 10), 1, 0)
	l := fynelayout.New(false, 0, 0)
	l.Layout([]fyne.CanvasObject{fixed, grows}, fyne.NewSize(100, 200))

	if got := fixed.Size().Height; got != 30 {
		t.Errorf("unweighted child got height %v, want its minimum 30", got)
	}
	if got := grows.Size().Height; got != 170 {
		t.Errorf("weighted child got height %v, want the remaining 170", got)
	}
}

// Padding insets every child, gap separates two of them, and a margin insets
// one -- all three come out of the space the weights then divide.
func TestPaddingGapAndMarginComeOutOfTheShare(t *testing.T) {
	a, b := shared(box(10, 10), 1, 5), shared(box(10, 10), 1, 0)
	l := fynelayout.New(true, 10, 20)
	l.Layout([]fyne.CanvasObject{a, b}, fyne.NewSize(200, 50))

	// 200 - 2*20 padding - 10 gap - 2*5 margin = 140, split evenly.
	if got := a.Size().Width; got != 70 {
		t.Errorf("first child got width %v, want 70", got)
	}
	if got := b.Size().Width; got != 70 {
		t.Errorf("second child got width %v, want 70", got)
	}
	if got := a.Position().X; got != 25 {
		t.Errorf("first child starts at %v, want padding 20 + margin 5", got)
	}
	if got := b.Position().X; got != 110 {
		t.Errorf("second child starts at %v, want 25 + 70 + 5 + 10", got)
	}
}

// MinSize is every child at its own minimum: a weight asks for a share of the
// surplus and says nothing about the floor.
func TestMinSizeSumsTheChildren(t *testing.T) {
	l := fynelayout.New(false, 6, 4)
	got := l.MinSize([]fyne.CanvasObject{shared(box(30, 10), 1, 2), shared(box(50, 20), 1, 2)})

	// main: 10 + 4 + 20 + 4 + 6 gap + 2*4 padding
	if got.Height != 52 {
		t.Errorf("min height %v, want 52", got.Height)
	}
	// cross: widest child 50 + its 2*2 margin + 2*4 padding
	if got.Width != 62 {
		t.Errorf("min width %v, want 62", got.Width)
	}
}

// A box too small for its children hands out nothing rather than negative
// sizes, which Fyne would carry into a renderer.
func TestOverflowNeverGoesNegative(t *testing.T) {
	a, b := box(100, 10), shared(box(100, 10), 1, 0)
	l := fynelayout.New(true, 0, 0)
	l.Layout([]fyne.CanvasObject{a, b}, fyne.NewSize(50, 50))

	if got := b.Size().Width; got != 0 {
		t.Errorf("overflowing weighted child got width %v, want 0", got)
	}
}

// A share is the child's, not a position in the box: a render slot's anchor
// and content land among the children the box was built with, and a weight
// read by position went to the anchor while the scroll that asked for it was
// packed at its minimum.
func TestShareFollowsTheChildNotItsPosition(t *testing.T) {
	head, anchor, grows := box(10, 30), box(10, 10), shared(box(10, 10), 1, 0)
	anchor.Hide()
	l := fynelayout.New(false, 0, 0)
	l.Layout([]fyne.CanvasObject{head, anchor, grows}, fyne.NewSize(100, 200))

	if got := grows.Size().Height; got != 170 {
		t.Errorf("weighted child after an anchor got height %v, want the remaining 170", got)
	}
}

// One of Fyne's own boxes handed a child with a weight lays it out by its
// share, which is how a page a navigation stack inserts fills the window.
func TestInsertingAWeightedChildAdoptsTheBox(t *testing.T) {
	c := container.NewVBox(box(10, 30))
	anchor := fynelayout.SlotAnchor(c, nil)
	page := shared(box(10, 10), 1, 0)
	fynelayout.InsertBefore(c, anchor, page)
	c.Resize(fyne.NewSize(100, 200))

	pad := theme.Padding()
	if got, want := page.Size().Height, 200-30-pad; got != want {
		t.Errorf("inserted page got height %v, want %v", got, want)
	}
}

// A box handed only children without a weight stays Fyne's own.
func TestInsertingAFixedChildKeepsTheBox(t *testing.T) {
	c := container.NewVBox()
	fynelayout.InsertBefore(c, nil, box(10, 10))
	if _, ok := c.Layout.(fynelayout.Weighted); ok {
		t.Error("a box given no weighted child was made a Weighted one")
	}
}
