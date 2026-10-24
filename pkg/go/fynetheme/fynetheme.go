// Package fynetheme is SNGL's paint styling as a Fyne theme.
//
// Fyne has no per-widget styling: a color or a text size belongs to the app's
// theme. What it does have is container.NewThemeOverride, which gives one
// subtree a theme of its own -- so a set of style properties becomes a theme
// and the node it came from becomes the subtree it applies to.
//
// fynelayout.Box puts the widget it shows under one, built from its Look.
package fynetheme

import (
	"image/color"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Theme is one set of SNGL style properties, for the subtree a
// container.ThemeOverride applies it to. A property left unset falls through
// to whatever theme it wraps.
type Theme struct {
	fyne.Theme
	Background, Foreground  color.Color
	TextSize, Radius        float32
	Bold, Italic, Monospace bool
}

// New wraps base, the theme the styled widget would otherwise be under.
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
	if t.Monospace {
		s.Monospace = true
	}
	return t.Theme.Font(s)
}
