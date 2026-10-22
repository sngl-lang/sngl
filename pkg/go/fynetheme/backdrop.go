package fynetheme

import (
	"image/color"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ColorNameBox is the colour a node's own style paints behind it: its
// `background`, and transparent where the style gave none. It is a name of
// its own rather than the theme's background, which every theme answers.
const ColorNameBox fyne.ThemeColorName = "snglBox"

// Backdrop is a node drawn on its style's background. Fyne's theme colours no
// label's box, so a `ui.badge` was plain text: a rectangle behind the node is
// its box, painted in the colour the theme around it answers for
// ColorNameBox and rounded by its radius. The theme is read where the
// backdrop draws, so a theme override given a new theme and refreshed -- a
// background that is a run-time value -- repaints it.
type Backdrop struct {
	widget.BaseWidget
	Content fyne.CanvasObject
}

// NewBackdrop puts content on its background.
func NewBackdrop(content fyne.CanvasObject) *Backdrop {
	b := &Backdrop{Content: content}
	b.ExtendBaseWidget(b)
	return b
}

func (b *Backdrop) CreateRenderer() fyne.WidgetRenderer {
	r := &backdropRenderer{b: b, box: canvas.NewRectangle(color.Transparent)}
	r.paint()
	return r
}

type backdropRenderer struct {
	b   *Backdrop
	box *canvas.Rectangle
}

func (r *backdropRenderer) paint() {
	r.box.FillColor = theme.ColorForWidget(ColorNameBox, r.b)
	r.box.CornerRadius = theme.SizeForWidget(theme.SizeNameInputRadius, r.b)
}

func (r *backdropRenderer) Layout(size fyne.Size) {
	r.box.Resize(size)
	r.b.Content.Resize(size)
}

func (r *backdropRenderer) MinSize() fyne.Size { return r.b.Content.MinSize() }

func (r *backdropRenderer) Refresh() {
	r.paint()
	r.box.Refresh()
	r.b.Content.Refresh()
}

func (r *backdropRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.box, r.b.Content}
}

func (r *backdropRenderer) Destroy() {}
