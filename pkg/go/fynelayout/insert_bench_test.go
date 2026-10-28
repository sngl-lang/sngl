package fynelayout_test

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/fynelayout"
	"duckfam.us/sngl/pkg/go/snglcolor"
)

// BenchmarkSlotRenderUnderABox renders a fifteen-row table of five labels a
// row into a render slot's box, as a slot drawn again for each of fifteen
// answers does, and walks the box once after each render, as the next draw
// does. A boxed cell is measured as its row is built.
func BenchmarkSlotRenderUnderABox(b *testing.B) {
	for _, bc := range []struct{ themed, boxedCells bool }{{false, false}, {true, false}, {true, true}} {
		b.Run(fmt.Sprintf("themed=%v/boxedCells=%v", bc.themed, bc.boxedCells), func(b *testing.B) {
			c := fynelayout.NewVBox()
			box := fynelayout.NewBox(c)
			if bc.themed {
				box.SetLook(fynelayout.Look{TextColor: snglcolor.Color{R: 255, A: 255}})
			} else {
				box.SetLook(fynelayout.Look{PaddingTop: 1})
			}
			anchor := fynelayout.SlotAnchor(c, nil)
			w := fynetest.NewWindow(box)
			defer w.Close()
			w.Resize(fyne.NewSize(400, 800))
			var rows []fyne.CanvasObject
			for b.Loop() {
				for _, r := range rows {
					fynelayout.Remove(c, r)
				}
				rows = rows[:0]
				for i := range 15 {
					row := fynelayout.NewHBox()
					for j := range 5 {
						var cell fyne.CanvasObject = widget.NewLabel(fmt.Sprintf("cell %d %d", i, j))
						if bc.boxedCells {
							boxed := fynelayout.NewBox(cell)
							boxed.SetLook(fynelayout.Look{PaddingLeft: 2})
							cell = boxed
						}
						row.Add(cell)
					}
					fynelayout.InsertBefore(c, anchor, row)
					rows = append(rows, row)
				}
				fynetest.WidgetRenderer(box).Objects()
			}
		})
	}
}
