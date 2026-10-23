package fynetheme_test

import (
	"image/color"
	"testing"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"duckfam.us/sngl/pkg/go/fynetheme"
	"duckfam.us/sngl/pkg/go/snglcolor"
)

// box is the rectangle a backdrop paints behind its content.
func box(t *testing.T, b *fynetheme.Backdrop) *canvas.Rectangle {
	t.Helper()
	for _, o := range fynetest.WidgetRenderer(b).Objects() {
		if r, ok := o.(*canvas.Rectangle); ok {
			return r
		}
	}
	t.Fatal("the backdrop draws no rectangle")
	return nil
}

func same(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// A label with a background is drawn on a box of that color, rounded by the
// theme's radius, and the box is the label's size.
func TestBackdropPaintsTheBackground(t *testing.T) {
	fynetest.NewTempApp(t)
	label := widget.NewLabel("Grass")
	b := fynetheme.NewBackdrop(label)
	th := fynetheme.New(theme.DefaultTheme()).WithBackground(snglcolor.Color{R: 120, G: 200, B: 80, A: 255}).WithRadius(12)
	w := fynetest.NewWindow(container.NewThemeOverride(b, th))
	defer w.Close()
	w.Resize(fyne.NewSize(200, 100))

	r := box(t, b)
	if want := (color.NRGBA{R: 120, G: 200, B: 80, A: 255}); !same(r.FillColor, want) {
		t.Errorf("box painted %v, want the background %v", r.FillColor, want)
	}
	if r.CornerRadius != 12 {
		t.Errorf("box radius %v, want the style's 12", r.CornerRadius)
	}
	if b.MinSize() != label.MinSize() {
		t.Errorf("backdrop's minimum %v, want its label's %v", b.MinSize(), label.MinSize())
	}
}

// A background that is a run-time value is a new theme on the override,
// which repaints the box when it is refreshed.
func TestBackdropFollowsTheTheme(t *testing.T) {
	fynetest.NewTempApp(t)
	b := fynetheme.NewBackdrop(widget.NewLabel("Fire"))
	ov := container.NewThemeOverride(b, fynetheme.New(theme.DefaultTheme()).WithBackground(snglcolor.Color{R: 1, G: 2, B: 3, A: 255}))
	w := fynetest.NewWindow(ov)
	defer w.Close()

	ov.Theme = fynetheme.New(theme.DefaultTheme()).WithBackground(snglcolor.Color{R: 240, G: 128, B: 48, A: 255})
	ov.Refresh()
	if r, want := box(t, b), (color.NRGBA{R: 240, G: 128, B: 48, A: 255}); !same(r.FillColor, want) {
		t.Errorf("box painted %v after the theme changed, want %v", r.FillColor, want)
	}
}

// A theme whose style gave no background paints no box: the theme's own
// background is the window's, not the node's.
func TestBackdropWithoutBackgroundIsClear(t *testing.T) {
	fynetest.NewTempApp(t)
	b := fynetheme.NewBackdrop(widget.NewLabel("plain"))
	w := fynetest.NewWindow(container.NewThemeOverride(b, fynetheme.New(theme.DefaultTheme()).WithTextSize(20)))
	defer w.Close()
	if _, _, _, a := box(t, b).FillColor.RGBA(); a != 0 {
		t.Errorf("box painted %v with no background", box(t, b).FillColor)
	}
}
