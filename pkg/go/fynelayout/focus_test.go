package fynelayout_test

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/fynelayout"
)

// Tab passes over each NoTab widget, and a tap on one changes it as it
// changes the widget it embeds while focusing nothing.
func TestTabPassesOverANoTabWidget(t *testing.T) {
	fynetest.NewApp()
	checked := false
	check := fynelayout.NewNoTabCheck("c", func(on bool) { checked = on })
	pick := fynelayout.NewNoTabSelect([]string{"a", "b"}, nil)
	button := fynelayout.NewNoTabButton("b", nil)
	entry := widget.NewEntry()
	w := fynetest.NewWindow(container.NewVBox(check, pick, button, entry))
	defer w.Close()
	c := w.Canvas()

	for range 2 {
		fynetest.FocusNext(c)
		if c.Focused() != fyne.Focusable(entry) {
			t.Fatalf("Tab focused %T, want the entry", c.Focused())
		}
	}

	c.Unfocus()
	fynetest.Tap(check)
	if !checked || c.Focused() != nil {
		t.Fatalf("after a tap the check is %v and %T is focused", checked, c.Focused())
	}
}
