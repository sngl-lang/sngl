package fynelayout

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// The widgets a component written `focusable=false` is on this platform: the
// Fyne widget they embed, which Tab passes over. fyne's focus manager focuses
// no widget that reports itself disabled, whether moving focus with Tab or
// asked to by a tap, so each of these reports itself disabled to it. That is
// all the report reaches: the embedded widget's own methods ask the embedded
// widget, so it is drawn, tapped and changed as an enabled one is.
//
// A button takes no focus when tapped in any case, and a check or a select
// tapped takes none here, so a click leaves the keys to the window's `@key`.

// NoTabButton is a widget.Button Tab passes over.
type NoTabButton struct {
	widget.Button
}

// NewNoTabButton is widget.NewButton's NoTabButton.
func NewNoTabButton(label string, tapped func()) *NoTabButton {
	b := &NoTabButton{}
	b.Text, b.OnTapped = label, tapped
	b.ExtendBaseWidget(b)
	return b
}

// Disabled is what the focus manager asks.
func (b *NoTabButton) Disabled() bool { return true }

// NoTabCheck is a widget.Check Tab passes over.
type NoTabCheck struct {
	widget.Check
}

// NewNoTabCheck is widget.NewCheck's NoTabCheck.
func NewNoTabCheck(label string, changed func(bool)) *NoTabCheck {
	c := &NoTabCheck{}
	c.Text, c.OnChanged = label, changed
	c.ExtendBaseWidget(c)
	return c
}

// Disabled is what the focus manager asks.
func (c *NoTabCheck) Disabled() bool { return true }

// NoTabSelect is a widget.Select Tab passes over.
type NoTabSelect struct {
	widget.Select
}

// NewNoTabSelect is widget.NewSelect's NoTabSelect.
func NewNoTabSelect(options []string, changed func(string)) *NoTabSelect {
	s := &NoTabSelect{}
	s.Options, s.OnChanged = options, changed
	s.ExtendBaseWidget(s)
	return s
}

// Disabled is what the focus manager asks.
func (s *NoTabSelect) Disabled() bool { return true }

var (
	_ fyne.Disableable = (*NoTabButton)(nil)
	_ fyne.Disableable = (*NoTabCheck)(nil)
	_ fyne.Disableable = (*NoTabSelect)(nil)
)
