package canvas

import (
	"image/color"
	"testing"
)

func TestNewResultSize(t *testing.T) {
	c := New(40, 20)
	img := c.Result()
	b := img.Bounds()
	if b.Dx() != 40 || b.Dy() != 20 {
		t.Fatalf("Result size = %dx%d, want 40x20", b.Dx(), b.Dy())
	}
}

func nrgbaAt(t *testing.T, c *Context, x, y int) color.NRGBA {
	t.Helper()
	r, g, b, a := c.Result().At(x, y).RGBA()
	return color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func TestRectFills(t *testing.T) {
	c := New(20, 20)
	c.ApplyStyle(Style{Fill: color.NRGBA{255, 0, 0, 255}})
	c.Rect(5, 5, 10, 10)
	if got := nrgbaAt(t, c, 10, 10); got.R != 255 || got.G != 0 || got.B != 0 {
		t.Errorf("center pixel = %+v, want red", got)
	}
	if got := nrgbaAt(t, c, 0, 0); got.A != 0 {
		t.Errorf("corner pixel = %+v, want transparent (no fill)", got)
	}
}

func TestLineStrokesOnly(t *testing.T) {
	c := New(20, 20)
	c.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 255, 255}, StrokeWidth: 2})
	c.Line(0, 10, 20, 10)
	if got := nrgbaAt(t, c, 10, 10); got.B != 255 {
		t.Errorf("on-line pixel = %+v, want blue", got)
	}
}

func TestPathFills(t *testing.T) {
	c := New(20, 20)
	c.ApplyStyle(Style{Fill: color.NRGBA{0, 255, 0, 255}})
	c.MoveTo(2, 2)
	c.LineTo(18, 2)
	c.LineTo(18, 18)
	c.ClosePath()
	c.PaintPath()
	if got := nrgbaAt(t, c, 15, 10); got.G != 255 {
		t.Errorf("inside-triangle pixel = %+v, want green", got)
	}
}

func TestSettersDriveFill(t *testing.T) {
	c := New(10, 10)
	c.SetFill(0, 255, 0, 255)
	c.Rect(0, 0, 10, 10)
	if got := nrgbaAt(t, c, 5, 5); got.G != 255 {
		t.Errorf("center = %+v, want green via SetFill", got)
	}
}

func TestSettersDriveStroke(t *testing.T) {
	c := New(20, 20)
	c.SetStroke(0, 0, 255, 255)
	c.SetStrokeWidth(2)
	c.Line(0, 10, 20, 10)
	if got := nrgbaAt(t, c, 10, 10); got.B != 255 {
		t.Errorf("on-line pixel = %+v, want blue via setters", got)
	}
}

func TestTextDoesNotPanic(t *testing.T) {
	c := New(60, 20)
	c.ApplyStyle(Style{Fill: color.NRGBA{0, 0, 0, 255}, FontSize: 12})
	c.Text(2, 14, "hi") // gg uses a built-in basic font when none is set.
}
