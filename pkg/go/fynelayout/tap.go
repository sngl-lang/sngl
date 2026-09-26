package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// Tap shows one object and reports a tap on it. A SNGL canvas is a
// canvas.Image or canvas.Raster, which are drawn and never tapped: Fyne hands
// a tap only to a widget that implements fyne.Tappable.
type Tap struct {
	widget.BaseWidget
	Content  fyne.CanvasObject
	OnTapped func()
}

func NewTap(content fyne.CanvasObject) *Tap {
	t := &Tap{Content: content}
	t.ExtendBaseWidget(t)
	return t
}

func (t *Tap) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.Content)
}

func (t *Tap) Tapped(*fyne.PointEvent) {
	if t.OnTapped != nil {
		t.OnTapped()
	}
}
