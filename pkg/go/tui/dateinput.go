package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// dateLayout is how a date is typed: the YYYY-MM-DD a SNGL date literal is.
const dateLayout = "2006-01-02"

// DateInput is sngl:ui's datepicker in a terminal: a text field a date is
// typed into, read as the *time.Time an option<date> is in Go.
type DateInput struct {
	textinput.Model
}

// NewDateInput returns an empty field that takes at most a date's ten
// characters.
func NewDateInput() DateInput {
	m := textinput.New()
	m.CharLimit = len(dateLayout)
	return DateInput{Model: m}
}

// Date is the date the field holds, or nil while what is typed is not one.
func (d DateInput) Date() *time.Time {
	t, err := time.Parse(dateLayout, strings.TrimSpace(d.Value()))
	if err != nil {
		return nil
	}
	return &t
}

// SetDate shows t, or clears the field for nil. A binding writes back what
// Date read, so a date the field already says -- and nil while the text is
// half a date -- leaves the text as it is: clearing it would undo the typing.
func (d *DateInput) SetDate(t *time.Time) {
	cur := d.Date()
	switch {
	case t == nil && cur == nil:
		return
	case t != nil && cur != nil && t.Equal(*cur):
		return
	case t == nil:
		d.SetValue("")
	default:
		d.SetValue(t.Format(dateLayout))
	}
}

// Update is textinput's, returning the field so it stays a DateInput.
func (d DateInput) Update(msg tea.Msg) (DateInput, tea.Cmd) {
	m, cmd := d.Model.Update(msg)
	return DateInput{Model: m}, cmd
}
