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

// PathCmd mirrors the SNGL stdlib PathCmd (op + coordinates).
type PathCmd struct {
	Op       string // "moveTo" | "lineTo" | "bezierTo" | "arcTo" | "close"
	X, Y     float64
	Cx1, Cy1 float64
	Cx2, Cy2 float64
	R        float64
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
