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

// countOpaque returns the number of non-transparent pixels in the image.
func countOpaque(c *Context) int {
	img := c.Result()
	b := img.Bounds()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				n++
			}
		}
	}
	return n
}

// TestFontSizeHonored: a bigger FontSize must produce strictly more glyph
// pixels for the same string, proving the pending FontSize drives rendering
// rather than gg's fixed basic font.
func TestFontSizeHonored(t *testing.T) {
	small := New(200, 80)
	small.ApplyStyle(Style{Fill: color.NRGBA{0, 0, 0, 255}, FontSize: 8})
	small.Text(2, 40, "size")

	big := New(200, 80)
	big.ApplyStyle(Style{Fill: color.NRGBA{0, 0, 0, 255}, FontSize: 30})
	big.Text(2, 40, "size")

	ns, nb := countOpaque(small), countOpaque(big)
	if ns == 0 {
		t.Fatalf("small text drew no pixels")
	}
	if nb <= ns {
		t.Errorf("font size not honored: size 30 drew %d px, size 8 drew %d px (want strictly more)", nb, ns)
	}
}

// TestArcToRoundsCorner: an arcTo from a horizontal lead-in to a vertical
// lead-out rounds the corner at the control point. The rounded path stays
// inside the square corner — so a pixel deep in the corner (where a sharp
// lineTo would draw) is NOT stroked, while a pixel on the arc IS.
func TestArcToRoundsCorner(t *testing.T) {
	c := New(40, 40)
	c.ApplyStyle(Style{Stroke: color.NRGBA{255, 0, 0, 255}, StrokeWidth: 2})
	// P0=(0,30) -> approach corner P1=(30,30) -> head to P2=(30,0), r=15.
	c.MoveTo(0, 30)
	c.ArcTo(30, 30, 30, 0, 15)
	c.LineTo(30, 0)
	c.PaintPath()

	// The arc is tangent to both segments; the inner corner near P1=(30,30)
	// is cut off, so deep in the corner there is no stroke.
	if got := nrgbaAt(t, c, 29, 29); got.R != 0 {
		t.Errorf("corner pixel = %+v, want unstroked (arc rounds the corner)", got)
	}
	// Somewhere along the lead-in segment the stroke is present.
	if got := nrgbaAt(t, c, 5, 30); got.R == 0 {
		t.Errorf("lead-in pixel = %+v, want stroked (red)", got)
	}
}

// TestArcToDegenerate: collinear points, zero radius, and a missing current
// point must degrade to a straight line without panicking.
func TestArcToDegenerate(t *testing.T) {
	// Collinear P0,P1,P2.
	c1 := New(20, 20)
	c1.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 0, 255}, StrokeWidth: 1})
	c1.MoveTo(0, 10)
	c1.ArcTo(10, 10, 20, 10, 5)
	c1.PaintPath()

	// Zero radius.
	c2 := New(20, 20)
	c2.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 0, 255}, StrokeWidth: 1})
	c2.MoveTo(0, 0)
	c2.ArcTo(10, 10, 19, 0, 0)
	c2.PaintPath()

	// No current point set before ArcTo.
	c3 := New(20, 20)
	c3.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 0, 255}, StrokeWidth: 1})
	c3.ArcTo(5, 5, 15, 15, 5)
	c3.PaintPath()
}

// TestLineCapRound: a thick round-capped horizontal line extends past its
// endpoint by ~half the stroke width; a butt cap does not. The pixel just
// beyond the endpoint distinguishes the two cap styles.
func TestLineCapRound(t *testing.T) {
	const w = 10 // stroke width; round cap reaches ~5px past the endpoint
	round := New(40, 20)
	round.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 255, 255}, StrokeWidth: w, LineCap: "round"})
	round.Line(10, 10, 30, 10)

	butt := New(40, 20)
	butt.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 255, 255}, StrokeWidth: w, LineCap: "butt"})
	butt.Line(10, 10, 30, 10)

	// 2px beyond the right endpoint (x=32), on the line's centerline.
	if got := nrgbaAt(t, round, 32, 10); got.B == 0 {
		t.Errorf("round cap: pixel beyond endpoint = %+v, want stroked (blue)", got)
	}
	if got := nrgbaAt(t, butt, 32, 10); got.B != 0 {
		t.Errorf("butt cap: pixel beyond endpoint = %+v, want unstroked", got)
	}
}
