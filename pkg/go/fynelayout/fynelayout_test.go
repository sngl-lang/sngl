package fynelayout_test

import (
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	fynetest "fyne.io/fyne/v2/test"

	"git.duckfam.us/jonathan/sngl/pkg/go/fynelayout"
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

func TestWeightsSplitTheSurplus(t *testing.T) {
	a, b := box(10, 10), box(10, 10)
	l := fynelayout.New(true, []float32{1, 3}, nil, 0, 0)
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
	fixed, grows := box(10, 30), box(10, 10)
	l := fynelayout.New(false, []float32{0, 1}, nil, 0, 0)
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
	a, b := box(10, 10), box(10, 10)
	l := fynelayout.New(true, []float32{1, 1}, []float32{5, 0}, 10, 20)
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
	l := fynelayout.New(false, []float32{1, 1}, []float32{2, 2}, 6, 4)
	got := l.MinSize([]fyne.CanvasObject{box(30, 10), box(50, 20)})

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
	a, b := box(100, 10), box(100, 10)
	l := fynelayout.New(true, []float32{0, 1}, nil, 0, 0)
	l.Layout([]fyne.CanvasObject{a, b}, fyne.NewSize(50, 50))

	if got := b.Size().Width; got != 0 {
		t.Errorf("overflowing weighted child got width %v, want 0", got)
	}
}
