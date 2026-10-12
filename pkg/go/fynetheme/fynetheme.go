// Package fynetheme is SNGL's paint styling as a Fyne theme.
//
// Fyne has no per-widget styling: a colour or a text size belongs to the app's
// theme. What it does have is container.NewThemeOverride, which gives one
// subtree a theme of its own -- so a set of style properties becomes a theme
// and the node it came from becomes the subtree it applies to.
//
// It is a runtime package because two things need it and neither should own
// it: the fyne emitter, which wraps a node in one, and the interpreted host,
// which does the same at runtime from the same style record.
package fynetheme

import (
	"image/color"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"duckfam.us/sngl/pkg/go/snglcolor"
)

// Theme is one set of SNGL style properties, for the subtree a
// container.ThemeOverride applies it to. A property left unset falls through
// to whatever theme it wraps.
type Theme struct {
	fyne.Theme
	Background, Foreground color.Color
	TextSize, Radius       float32
	Bold, Italic           bool
}

// New wraps base, which is normally the app's current theme.
func New(base fyne.Theme) Theme { return Theme{Theme: base} }

func (t Theme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameButton, theme.ColorNameBackground, theme.ColorNameInputBackground:
		if t.Background != nil {
			return t.Background
		}
	case theme.ColorNameForeground:
		if t.Foreground != nil {
			return t.Foreground
		}
	}
	return t.Theme.Color(n, v)
}

func (t Theme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		if t.TextSize > 0 {
			return t.TextSize
		}
	case theme.SizeNameInputRadius, "buttonRadius":
		if t.Radius > 0 {
			return t.Radius
		}
	}
	return t.Theme.Size(n)
}

func (t Theme) Font(s fyne.TextStyle) fyne.Resource {
	if t.Bold {
		s.Bold = true
	}
	if t.Italic {
		s.Italic = true
	}
	return t.Theme.Font(s)
}

// Empty reports whether this theme asks for nothing, in which case wrapping a
// node in it costs a node and buys nothing.
func (t Theme) Empty() bool {
	return t.Background == nil && t.Foreground == nil && t.TextSize == 0 && t.Radius == 0 && !t.Bold && !t.Italic
}

// WithBackground and the With methods after it build a theme from style fields
// known only at run time, each taking its value the way generated code holds it.
func (t Theme) WithBackground(c snglcolor.Color) Theme {
	t.Background = nrgba(c)
	return t
}

func (t Theme) WithForeground(c snglcolor.Color) Theme {
	t.Foreground = nrgba(c)
	return t
}

func (t Theme) WithTextSize(v float64) Theme {
	t.TextSize = float32(v)
	return t
}

func (t Theme) WithRadius(v float64) Theme {
	t.Radius = float32(v)
	return t
}

// WithWeight and WithSlant take the enum member's name, which is how Go holds
// a SNGL enum. Fyne has two faces per axis: bold or regular, italic or upright.
func (t Theme) WithWeight(member string) Theme {
	t.Bold = member == "bold"
	return t
}

func (t Theme) WithSlant(member string) Theme {
	t.Italic = member == "italic"
	return t
}

func nrgba(c snglcolor.Color) color.Color {
	return color.NRGBA{R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}
}
