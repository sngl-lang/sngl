package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Select is a select: a button showing the label chosen, which opens a
// popover listing the choices below it. The value is the label chosen, which
// is what the program binds, rather than the row a cursor is on; it is held
// even when no choice carries it, since a program's choices may arrive after
// its value -- the choices of a request's answer -- and the cursor finds it
// once they do.
//
// Closed, Enter or Space opens it and Up and Down choose the row before or
// after. Open, Up and Down move the cursor, Enter or a click on a row chooses
// and closes it, and Esc closes it leaving the value as it was; Tab and a
// click outside close it from the program's Update (Floats).
type Select struct {
	options []string
	value   string
	cursor  int
	rows    int
	width   int
	// st is what every copy of this Select shares, as a program's Model
	// copies it: whether it is open, which Floats closes from outside it.
	st *selectState
}

type selectState struct {
	open    bool
	focused bool
	// first is the row the popover starts at, kept so the cursor scrolls it
	// only once it reaches an edge.
	first int
}

// NewSelect builds a Select offering nothing, with nothing chosen, at most w
// cells wide and showing at most h choices at once: its choices arrive with
// each view and update.
func NewSelect(w, h int) Select {
	s := Select{st: &selectState{}}
	s.SetSize(w, h)
	return s
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
	s.cursor = min(s.cursor, max(len(options)-1, 0))
	s.moveTo(s.value)
}

func (s *Select) moveTo(v string) {
	if i := slices.Index(s.options, v); i >= 0 {
		s.cursor = i
	}
}

// Focus and Blur say whether focus is on it: its button is drawn so, and one
// it leaves closes.
func (s *Select) Focus() { s.st.focused = true }

func (s *Select) Blur() {
	s.st.focused = false
	s.st.open = false
}

// Open reports whether its popover is showing.
func (s Select) Open() bool { return s.st.open }

// Update offers options, then answers a key or a click on the popover. Any
// other message leaves the value alone, a value no choice carries included.
// The choices are a prop the program may change while the widget stands, so
// each message brings them; f is the program's Floats, which the popover is
// drawn by and a click on it asked of.
func (s Select) Update(msg tea.Msg, options []string, f *Floats) (Select, tea.Cmd) {
	s.SetOptions(options)
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s.st.open {
			s.openKey(msg)
		} else {
			s.closedKey(msg, f)
		}
	case tea.MouseClickMsg:
		if row, ok := f.rowAt(s.st, msg.X, msg.Y); ok && s.st.open && row < len(s.options) {
			s.choose(row)
		}
	}
	return s, nil
}

func (s *Select) closedKey(k tea.KeyPressMsg, f *Floats) {
	switch {
	case k.Code == tea.KeyEnter || k.Code == tea.KeySpace:
		s.moveTo(s.value)
		s.st.open = true
		s.st.first = max(s.cursor-s.rows+1, 0)
		f.track(s.st)
	case len(s.options) == 0:
	case k.Code == tea.KeyDown:
		s.value = s.options[min(s.chosenOr(-1)+1, len(s.options)-1)]
		s.moveTo(s.value)
	case k.Code == tea.KeyUp:
		s.value = s.options[max(s.chosenOr(len(s.options))-1, 0)]
		s.moveTo(s.value)
	}
}

// chosenOr is the row the value is, or none where no choice carries it.
func (s Select) chosenOr(none int) int {
	if i := slices.Index(s.options, s.value); i >= 0 {
		return i
	}
	return none
}

func (s *Select) openKey(k tea.KeyPressMsg) {
	switch {
	case k.Code == tea.KeyEsc:
		s.st.open = false
		s.moveTo(s.value)
	case k.Code == tea.KeyEnter || k.Code == tea.KeySpace:
		if s.cursor < len(s.options) {
			s.choose(s.cursor)
		}
	case k.Code == tea.KeyDown:
		s.cursor = min(s.cursor+1, max(len(s.options)-1, 0))
	case k.Code == tea.KeyUp:
		s.cursor = max(s.cursor-1, 0)
	case k.Code == tea.KeyHome:
		s.cursor = 0
	case k.Code == tea.KeyEnd:
		s.cursor = max(len(s.options)-1, 0)
	}
	if s.cursor < s.st.first {
		s.st.first = s.cursor
	}
	if s.cursor >= s.st.first+s.rows {
		s.st.first = s.cursor - s.rows + 1
	}
}

func (s *Select) choose(row int) {
	s.cursor = row
	s.value = s.options[row]
	s.st.open = false
}

// selectButton is the box a closed select is drawn in, and an open one's
// button, as a button's is: focused, it is marked `>` and its edge is bold.
func selectButton(focused bool, label string) string {
	mark := " "
	if focused {
		mark = ">"
	}
	return Focused(ButtonLook(lipgloss.NewStyle()), focused).Render(mark + " " + label)
}

// View draws the button, and where it is open hands f the popover to draw
// below it.
func (s Select) View(options []string, f *Floats) string {
	s.SetOptions(options)
	button := selectButton(s.st.focused, s.clip(s.value)+" ▾")
	if !s.st.open {
		return button
	}
	return f.Float(s.st, button, s.popover())
}

// clip is l cut to what the select's width leaves a label.
func (s Select) clip(l string) string {
	if s.width <= 0 {
		return l
	}
	return lipgloss.NewStyle().MaxWidth(max(s.width-7, 1)).Render(l)
}

// popover is the choices from the row the popover starts at, the cursor's
// reversed and the value's marked.
func (s Select) popover() string {
	end := min(s.st.first+s.rows, len(s.options))
	var lines []string
	for i := s.st.first; i < end; i++ {
		mark := "  "
		if s.options[i] == s.value {
			mark = "• "
		}
		line := mark + s.clip(s.options[i])
		if i == s.cursor {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = []string{"  "}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).PaddingRight(1).Render(strings.Join(lines, "\n"))
}

// SetSize bounds the select: its labels to w cells, its popover to h
// choices and never more than ten.
func (s *Select) SetSize(w, h int) {
	s.width = w
	s.rows = max(min(h, 10), 1)
}
