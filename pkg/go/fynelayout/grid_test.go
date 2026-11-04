package fynelayout_test

import (
	"testing"

	"fyne.io/fyne/v2"

	"duckfam.us/sngl/pkg/go/fynelayout"
)

func TestGridColumnsShareTheWidth(t *testing.T) {
	kids := []fyne.CanvasObject{box(10, 10), box(40, 20), box(10, 10), box(10, 30), box(10, 10)}
	l := &fynelayout.Grid{Columns: 3, Gap: 10}
	if got, want := l.MinSize(kids), fyne.NewSize(140, 60); got != want {
		t.Errorf("min size %v, want %v: three of the widest, two gaps; rows of 20 and 30 and a gap", got, want)
	}
	l.Layout(kids, fyne.NewSize(320, 100))
	for i, want := range []fyne.Position{{X: 0, Y: 0}, {X: 110, Y: 0}, {X: 220, Y: 0}, {X: 0, Y: 30}, {X: 110, Y: 30}} {
		if got := kids[i].Position(); got != want {
			t.Errorf("child %d at %v, want %v", i, got, want)
		}
	}
	if got, want := kids[3].Size(), fyne.NewSize(100, 30); got != want {
		t.Errorf("a column is %v, want %v: the width less two gaps, in three", got, want)
	}
}

func TestGridFlowsWhereItHasNoColumns(t *testing.T) {
	kids := []fyne.CanvasObject{box(30, 10), box(30, 10), box(30, 20), box(50, 10)}
	l := &fynelayout.Grid{Gap: 5}
	l.Layout(kids, fyne.NewSize(70, 100))
	for i, want := range []fyne.Position{{X: 0, Y: 0}, {X: 35, Y: 0}, {X: 0, Y: 15}, {X: 0, Y: 40}} {
		if got := kids[i].Position(); got != want {
			t.Errorf("child %d at %v, want %v", i, got, want)
		}
	}
	if got, want := kids[1].Size(), fyne.NewSize(30, 10); got != want {
		t.Errorf("a flowing child is %v, want its minimum %v", got, want)
	}
	if got, want := l.MinSize(kids), fyne.NewSize(50, 50); got != want {
		t.Errorf("min size %v, want %v: its widest child, and the three rows the width it was laid out at holds", got, want)
	}
}
