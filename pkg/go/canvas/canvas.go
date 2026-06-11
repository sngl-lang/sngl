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
