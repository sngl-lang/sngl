// Package canvas is a small immediate-mode 2D drawing runtime backing SNGL's
// Canvas2D on Go platforms (fyne, bubbletea). It wraps github.com/fogleman/gg
// and is stateful: ApplyStyle sets the pending style, then a primitive call
// (Rect, Circle, ...) fills and/or strokes it. Generated code drives this; it
// is not meant as a public API.
package canvas

import (
	"image"
	"image/color"

	"github.com/fogleman/gg"
)

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

// Result returns the rendered image.
func (c *Context) Result() image.Image { return c.dc.Image() }

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

// PaintPath fills+strokes the path built via MoveTo/LineTo/CubicTo/ClosePath.
func (c *Context) PaintPath() { c.paint() }

func (c *Context) Text(x, y float64, content string) {
	s := c.pending
	if s.Fill.A > 0 {
		c.dc.SetColor(s.Fill)
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
