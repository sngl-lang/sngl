package tui

import (
	"slices"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Select is a select's list of choices with its value: the label chosen,
// which is what the program binds, rather than the row the cursor is on. The
// value is held even when no choice carries it, since a program's choices may
// arrive after its value -- the choices of a request's answer -- and the
// cursor finds it once they do.
type Select struct {
	list    list.Model
	options []string
	value   string
}

// NewSelect builds a Select offering nothing, with nothing chosen: its
// choices arrive with each view and update.
func NewSelect(w, h int) Select {
	return Select{list: NewList(nil, w, h)}
}

// Value is the label chosen.
func (s Select) Value() string { return s.value }

// SetValue chooses the label v, moving the cursor to it where a choice
// carries it.
func (s *Select) SetValue(v string) {
	s.value = v
	s.moveTo(v)
}

// SetOptions offers options in place of the choices before, keeping the
// value. Offering the same choices again does nothing, so it is cheap to call
// on every frame.
func (s *Select) SetOptions(options []string) {
	if slices.Equal(s.options, options) {
		return
	}
	s.options = slices.Clone(options)
	s.list.SetItems(StringItems(options))
	s.moveTo(s.value)
}

func (s *Select) moveTo(v string) {
	if i := slices.Index(s.options, v); i >= 0 {
		s.list.Select(i)
	}
}

// Update offers options, then moves the cursor as the list does, and the
// choice with it: a row the cursor moves to is chosen, and so is the row it
// is on when Enter is pressed -- the only way to choose the row it starts on
// where the value is no choice's. Any other message leaves the value alone, a
// value no choice carries included. The choices are a prop the program may
// change while the widget stands, so each message brings them.
func (s Select) Update(msg tea.Msg, options []string) (Select, tea.Cmd) {
	s.SetOptions(options)
	before := s.list.Index()
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	k, enter := msg.(tea.KeyPressMsg)
	enter = enter && k.Code == tea.KeyEnter
	if len(s.options) > 0 && (s.list.Index() != before || enter) {
		s.value = SelectedString(s.list)
	}
	return s, cmd
}

// View draws options with the cursor on the value.
func (s Select) View(options []string) string {
	s.SetOptions(options)
	return s.list.View()
}

// SetSize sizes the list of choices.
func (s *Select) SetSize(w, h int) { s.list.SetSize(w, h) }
