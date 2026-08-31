// Package canvas is a small immediate-mode 2D drawing runtime backing SNGL's
// Canvas2D on Go platforms (fyne, bubbletea). It wraps github.com/fogleman/gg
// and is stateful: ApplyStyle sets the pending style, then a primitive call
// (Rect, Circle, ...) fills and/or strokes it. Generated code drives this; it
// is not meant as a public API.
package canvas

import (
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font/gofont/goregular"
)

// goFont is the parsed embedded Go Regular TrueType font, used to render Text
// at the pending FontSize. Parsed once. If parsing fails goFont stays nil and
// Text falls back to gg's fixed basic font.
var (
	goFontOnce sync.Once
	goFont     *truetype.Font
)

func loadGoFont() *truetype.Font {
	goFontOnce.Do(func() {
		if f, err := truetype.Parse(goregular.TTF); err == nil {
			goFont = f
		}
	})
	return goFont
}

// ggLineCap maps SNGL line-cap names to gg constants. Default ("" / unknown)
// is butt, matching the Canvas2D default.
func ggLineCap(s string) gg.LineCap {
	switch s {
	case "round":
		return gg.LineCapRound
	case "square":
		return gg.LineCapSquare
	default: // "butt", "", unknown
		return gg.LineCapButt
	}
}

// ggLineJoin maps SNGL line-join names to gg constants. gg v1.3.0 has only
// Round and Bevel — it lacks a true miter join, so "miter" (the Canvas2D
// default) and unknown values map to Round.
func ggLineJoin(s string) gg.LineJoin {
	switch s {
	case "bevel":
		return gg.LineJoinBevel
	default: // "miter", "round", "", unknown — gg has no miter; use round
		return gg.LineJoinRound
	}
}

// Style is the resolved per-shape paint state. A zero-alpha Fill or Stroke
// disables that paint (matches the SNGL CanvasStyle transparent default).
type Style struct {
	Fill        color.NRGBA
	Stroke      color.NRGBA
	StrokeWidth float64
	LineCap     string
	LineJoin    string
	FontSize    float64
	FontFamily  string
}

// Context is a stateful 2D drawing surface.
type Context struct {
	dc      *gg.Context
	pending Style
}

// New creates a Context backed by a width x height pixel image.
func New(width, height int) *Context {
	return &Context{dc: gg.NewContext(width, height)}
}

// NewScaled creates a Context of pw x ph pixels that a drawing placed in a
// w x h coordinate space is scaled into, per mode.
//
// The point of it is that the shapes are rasterised at the size they are shown
// rather than drawn small and resampled up: an edge lands where it lands
// instead of being interpolated from where it landed at another size. mode is
// the SNGL scalingMode -- "fit", "fill", "stretch", or "" for none.
func NewScaled(pw, ph int, w, h float64, mode string) *Context {
	c := New(pw, ph)
	if w <= 0 || h <= 0 || mode == "" {
		return c
	}
	applyScale(c, pw, ph, w, h, mode)
	return c
}

// applyScale is the transform NewScaled and Surface.Begin both need.
func applyScale(c *Context, pw, ph int, w, h float64, mode string) {
	if w <= 0 || h <= 0 || mode == "" {
		return
	}
	sx, sy := float64(pw)/w, float64(ph)/h
	switch mode {
	case "stretch":
	case "fill":
		sx = math.Max(sx, sy)
		sy = sx
	default: // fit
		sx = math.Min(sx, sy)
		sy = sx
	}
	c.dc.Scale(sx, sy)
}

// Result returns the rendered image.
func (c *Context) Result() image.Image { return c.dc.Image() }

// Surface is a drawing target that outlives the drawing.
//
// A host that hands its renderer a picture -- Fyne's Raster, which asks for
// one at the pixel size it is about to upload -- calls back on every redraw,
// and allocating a fresh buffer each time throws away the whole image per
// keypress. This keeps it, and reallocates only when the size it is asked for
// changes.
//
// The zero value is ready to use. Not safe for concurrent use: a canvas is
// drawn by the UI thread that owns it.
type Surface struct {
	ctx    *Context
	pw, ph int
}

// Begin returns a cleared context of pw x ph pixels, with a drawing placed in
// a w x h coordinate space scaled into it per mode. The buffer is reused
// whenever the pixel size is unchanged, which is every redraw that is not a
// resize.
func (s *Surface) Begin(pw, ph int, w, h float64, mode string) *Context {
	if s.ctx == nil || s.pw != pw || s.ph != ph {
		s.ctx, s.pw, s.ph = NewScaled(pw, ph, w, h, mode), pw, ph
		return s.ctx
	}
	// Reused, so the last drawing is still in it. Zeroing the pixels is the
	// clear -- gg's own fills with the current colour, which is a paint.
	if img, ok := s.ctx.dc.Image().(*image.RGBA); ok {
		clear(img.Pix)
	}
	s.ctx.dc.Identity()
	applyScale(s.ctx, pw, ph, w, h, mode)
	return s.ctx
}

// Save / Restore bracket transform/clip state.
func (c *Context) Save()    { c.dc.Push() }
func (c *Context) Restore() { c.dc.Pop() }

// ApplyStyle sets the style used by the next primitive.
func (c *Context) ApplyStyle(s Style) { c.pending = s }

// Style setters used by generated code (avoids emitting struct literals).
// Color params are int to match the generated Color struct fields.
func (c *Context) SetFill(r, g, b, a int) {
	c.pending.Fill = color.NRGBA{uint8(r), uint8(g), uint8(b), uint8(a)}
}
func (c *Context) SetStroke(r, g, b, a int) {
	c.pending.Stroke = color.NRGBA{uint8(r), uint8(g), uint8(b), uint8(a)}
}
func (c *Context) SetStrokeWidth(w float64) { c.pending.StrokeWidth = w }
func (c *Context) SetFont(size float64, family string) {
	c.pending.FontSize, c.pending.FontFamily = size, family
}
func (c *Context) SetLineStyle(cap, join string) {
	c.pending.LineCap, c.pending.LineJoin = cap, join
}

// paint fills (when Fill.A>0) then strokes (when Stroke.A>0) the current path.
// FillPreserve keeps the path for the subsequent stroke.
func (c *Context) paint() {
	s := c.pending
	if s.Fill.A > 0 {
		c.dc.SetColor(s.Fill)
		if s.Stroke.A > 0 {
			c.dc.FillPreserve()
		} else {
			c.dc.Fill()
		}
	}
	if s.Stroke.A > 0 {
		if s.StrokeWidth > 0 {
			c.dc.SetLineWidth(s.StrokeWidth)
		}
		c.dc.SetLineCap(ggLineCap(s.LineCap))
		c.dc.SetLineJoin(ggLineJoin(s.LineJoin))
		c.dc.SetColor(s.Stroke)
		c.dc.Stroke()
	}
	c.dc.ClearPath()
}

// strokeOnly strokes the current path with the stroke color (used for lines).
func (c *Context) strokeOnly() {
	s := c.pending
	if s.Stroke.A > 0 {
		if s.StrokeWidth > 0 {
			c.dc.SetLineWidth(s.StrokeWidth)
		}
		c.dc.SetLineCap(ggLineCap(s.LineCap))
		c.dc.SetLineJoin(ggLineJoin(s.LineJoin))
		c.dc.SetColor(s.Stroke)
	}
	c.dc.Stroke()
}

func (c *Context) Rect(x, y, w, h float64)        { c.dc.DrawRectangle(x, y, w, h); c.paint() }
func (c *Context) Circle(cx, cy, r float64)       { c.dc.DrawCircle(cx, cy, r); c.paint() }
func (c *Context) Ellipse(cx, cy, rx, ry float64) { c.dc.DrawEllipse(cx, cy, rx, ry); c.paint() }
func (c *Context) Line(x1, y1, x2, y2 float64)    { c.dc.DrawLine(x1, y1, x2, y2); c.strokeOnly() }

// Path-builder methods. Generated code emits a loop over the SNGL PathCmd list,
// dispatching on the op to MoveTo/LineTo/CubicTo/ClosePath, then PaintPath to
// fill+stroke the built path per the pending style. This keeps the SNGL-side
// PathCmd slice on the generated-code side (no canvas.PathCmd crosses the
// boundary).
func (c *Context) MoveTo(x, y float64) { c.dc.MoveTo(x, y) }
func (c *Context) LineTo(x, y float64) { c.dc.LineTo(x, y) }
func (c *Context) CubicTo(x1, y1, x2, y2, x3, y3 float64) {
	c.dc.CubicTo(x1, y1, x2, y2, x3, y3)
}
func (c *Context) ClosePath() { c.dc.ClosePath() }

// ArcTo implements the Canvas2D arcTo: an arc of radius r tangent to the
// segment from the current point P0 to (x1,y1) and to the segment from
// (x1,y1) to (x2,y2). It adds a line from P0 to the first tangent point, then
// the arc to the second tangent point (matching how the HTML backend's
// ctx.arcTo(cx1,cy1,x,y,r) is driven). Degenerate inputs (no current point,
// coincident points, zero radius, or collinear points) fall back to a plain
// line to (x1,y1).
func (c *Context) ArcTo(x1, y1, x2, y2, r float64) {
	p0, ok := c.dc.GetCurrentPoint()
	if !ok {
		// No subpath yet: start one at the corner point.
		c.dc.MoveTo(x1, y1)
		return
	}
	x0, y0 := p0.X, p0.Y

	// Vectors from the corner P1 toward P0 and toward P2.
	d01x, d01y := x0-x1, y0-y1
	d21x, d21y := x2-x1, y2-y1
	l01 := math.Hypot(d01x, d01y)
	l21 := math.Hypot(d21x, d21y)
	if r <= 0 || l01 == 0 || l21 == 0 {
		c.dc.LineTo(x1, y1)
		return
	}
	// Unit vectors along each segment (away from the corner).
	u01x, u01y := d01x/l01, d01y/l01
	u21x, u21y := d21x/l21, d21y/l21

	// Half-angle between the two segments. cos = u01·u21.
	cosA := u01x*u21x + u01y*u21y
	if cosA > 1 {
		cosA = 1
	} else if cosA < -1 {
		cosA = -1
	}
	angle := math.Acos(cosA)
	// Collinear (straight through or doubling back): no arc fits.
	if angle <= 1e-9 || math.Abs(angle-math.Pi) <= 1e-9 {
		c.dc.LineTo(x1, y1)
		return
	}

	// Distance from the corner to each tangent point along the segments.
	tanLen := r / math.Tan(angle/2)
	// Tangent points on each segment.
	t1x, t1y := x1+u01x*tanLen, y1+u01y*tanLen
	t2x, t2y := x1+u21x*tanLen, y1+u21y*tanLen

	// Arc center: from the corner along the bisector of the two unit vectors,
	// at distance r/sin(angle/2). The interior bisector direction is the
	// normalized sum of the two unit vectors.
	bx, by := u01x+u21x, u01y+u21y
	bl := math.Hypot(bx, by)
	bx, by = bx/bl, by/bl
	centerDist := r / math.Sin(angle/2)
	cx, cy := x1+bx*centerDist, y1+by*centerDist

	// Start/end angles measured from the center to each tangent point.
	a1 := math.Atan2(t1y-cy, t1x-cx)
	a2 := math.Atan2(t2y-cy, t2x-cx)

	// Line to the first tangent point, then sweep the short way to the second.
	c.dc.LineTo(t1x, t1y)
	// Choose the sweep direction that connects t1->t2 the short way.
	if a2-a1 > math.Pi {
		a2 -= 2 * math.Pi
	} else if a1-a2 > math.Pi {
		a2 += 2 * math.Pi
	}
	c.dc.DrawArc(cx, cy, r, a1, a2)
}

// PaintPath fills+strokes the path built via MoveTo/LineTo/CubicTo/ClosePath.
func (c *Context) PaintPath() { c.paint() }

// Text draws content at (x,y) using the pending Fill color and FontSize.
// FontSize defaults to 16 when unset. Only the embedded Go Regular font is
// supported: the pending FontFamily is ignored, since resolving arbitrary
// family names requires a system font lookup that is out of scope. If the
// embedded font cannot be parsed, gg's fixed basic font is used instead.
func (c *Context) Text(x, y float64, content string) {
	s := c.pending
	if s.Fill.A > 0 {
		c.dc.SetColor(s.Fill)
	}
	size := s.FontSize
	if size <= 0 {
		size = 16
	}
	if f := loadGoFont(); f != nil {
		c.dc.SetFontFace(truetype.NewFace(f, &truetype.Options{Size: size}))
	}
	c.dc.DrawString(content, x, y)
}

// Image decodes src (a local file path) and draws it scaled into (x,y,w,h).
// A missing/undecodable path draws nothing.
func (c *Context) Image(x, y, w, h float64, src string) {
	img, err := gg.LoadImage(src)
	if err != nil {
		return
	}
	ib := img.Bounds()
	if ib.Dx() == 0 || ib.Dy() == 0 {
		return
	}
	sx := w / float64(ib.Dx())
	sy := h / float64(ib.Dy())
	c.Save()
	c.dc.Scale(sx, sy)
	c.dc.DrawImage(img, int(x/sx), int(y/sy))
	c.Restore()
}
