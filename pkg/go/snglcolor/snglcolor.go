// Package snglcolor is the Go representation of SNGL's built-in `color` type.
//
// SNGL models a color as RGBA with 8-bit channels stored as `int` (0-255),
// so Color mirrors that with int fields — this keeps the int arithmetic in
// the stdlib color helpers (color.lighten, color.darken, ...) compiling
// without per-channel conversions. Generated Go code constructs Color values
// directly (a `#rrggbb` literal lowers to `snglcolor.Color{R:..., ...}`) and
// relies on String() when a color is coerced to a string (e.g. a `text`
// value or a lipgloss style), matching the CSS form the html/JS backends emit.
package snglcolor

import "fmt"

// Color is RGBA with 8-bit channels held as int. A is the alpha channel,
// defaulting (via the SNGL stdlib) to 255 / fully opaque.
type Color struct {
	R int
	G int
	B int
	A int
}

// String renders the color as a CSS color string, matching
// internal/htmlutil.ColorExprToCSS: an opaque color becomes `#rrggbb`; a
// translucent color becomes `rgba(r,g,b,a)` with alpha scaled to 0..1.
func (c Color) String() string {
	if c.A < 255 {
		return fmt.Sprintf("rgba(%d,%d,%d,%g)", c.R, c.G, c.B, float64(c.A)/255)
	}
	return c.Hex()
}

// Hex renders the color as a `#rrggbb` string, dropping alpha. Mirrors the
// SNGL stdlib `color.hex`.
func (c Color) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}
