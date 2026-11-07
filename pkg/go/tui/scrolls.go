package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Scroll axes, as a scroll's `direction` names them.
const (
	ScrollVertical = iota
	ScrollHorizontal
	ScrollBoth
)

// Scrolls is the state of every scroll a view draws: how far each is
// scrolled, and how big its content and viewport were when last drawn. The
// program's Model holds one; a scroll is known by a key its view names it
// by, the node and the copy of each loop around it.
//
// A scroll's View is its content cut to its viewport at its offset, marked
// as a click's block is (Clicks), so the view's Scan finds where it was
// drawn: the wheel scrolls the smallest box under the pointer that moves
// along the wheel's axis, and the keys that page scroll the box focus was
// drawn in. A drag is the terminal's, which selects text with it.
type Scrolls struct {
	state map[string]*scrollState
	marks Clicks[string]
	// focused is the scroll the stop focus is on was drawn in, "" for none.
	focused string
	// follow asks the next view to scroll the focused stop into view: focus
	// moved by a key, which may have moved it out of sight.
	follow bool
}

type scrollState struct {
	axis               int
	x, y               int
	contentW, contentH int
	viewW, viewH       int
	// drawn says the last view drew it, and reports that its `@reachEnd`
	// has a handler; only a scroll both is is waited on.
	drawn, reports bool
	// due says the end of the content was within half a viewport of being
	// shown when it was last drawn, and reached at which content height it
	// was last reported at, -1 since it was scrolled away from.
	due     bool
	reached int
}

// NewScrolls is a Scrolls holding no scroll.
func NewScrolls() *Scrolls {
	return &Scrolls{state: map[string]*scrollState{}, marks: Clicks[string]{opens: 'y', closes: '~'}}
}

func (s *Scrolls) of(key string, axis int) *scrollState {
	st := s.state[key]
	if st == nil {
		st = &scrollState{axis: axis, reached: -1}
		s.state[key] = st
	}
	st.axis = axis
	return st
}

// Follow asks the next view to bring the stop focus moves to into view.
func (s *Scrolls) Follow() { s.follow = true }

// View is content, a scroll's whole, cut to the viewport w×h cells wide at
// the scroll's offset; a w or h of zero is a side the build gives no
// extent, which the scroll does not cut. focus is where in content the stop
// focus is on was drawn, where it is there (Clicks.Find), which the view
// scrolls to where focus has just moved; reports says the scroll's
// `@reachEnd` has a handler.
func (s *Scrolls) View(key, content string, w, h, axis int, focus Box, focused, reports bool) string {
	st := s.of(key, axis)
	st.drawn, st.reports = true, reports
	st.contentW, st.contentH = lipgloss.Width(content), lipgloss.Height(content)
	st.viewW, st.viewH = w, h
	vertical, horizontal := axis != ScrollHorizontal && h > 0, axis != ScrollVertical && w > 0
	if focused {
		s.focused = key
		if s.follow {
			if vertical {
				st.y = into(st.y, h, focus.Y0, focus.Y1)
			}
			if horizontal {
				st.x = into(st.x, w, focus.X0, focus.X1)
			}
		}
	}
	st.clamp()
	if vertical {
		lines := strings.Split(content, "\n")
		lines = lines[st.y:min(st.y+h, len(lines))]
		content = strings.Join(lines, "\n")
	}
	if horizontal {
		lines := strings.Split(content, "\n")
		for i, l := range lines {
			lines[i] = ansi.Cut(l, st.x, st.x+w)
		}
		content = strings.Join(lines, "\n")
	}
	st.due = st.atEnd()
	if !st.due {
		st.reached = -1
	}
	return s.marks.Mark(key, content)
}

// into is the offset that shows the span from a to b in a viewport n long
// now at offset at: unmoved where it shows already, else the least move.
func into(at, n, a, b int) int {
	switch {
	case a < at:
		return a
	case b >= at+n:
		return max(b-n+1, 0)
	}
	return at
}

func (st *scrollState) clamp() {
	if st.viewH > 0 {
		st.y = max(min(st.y, st.contentH-st.viewH), 0)
	} else {
		st.y = 0
	}
	if st.viewW > 0 {
		st.x = max(min(st.x, st.contentW-st.viewW), 0)
	} else {
		st.x = 0
	}
}

// atEnd reports whether the end of the content is within half a viewport of
// being shown: the bottom, or the right edge of a horizontal scroll. A side
// with no extent shows its end.
func (st *scrollState) atEnd() bool {
	if st.axis == ScrollHorizontal {
		return st.viewW <= 0 || st.contentW-(st.x+st.viewW) <= st.viewW/2
	}
	return st.viewH <= 0 || st.contentH-(st.y+st.viewH) <= st.viewH/2
}

// Scan is v without the scrolls' marks, keeping where each was drawn with
// v's top left corner at column x, row y, as Clicks.Scan does.
func (s *Scrolls) Scan(v string, x, y int) string { return s.marks.Scan(v, x, y) }

// Settle ends a view: the follow it asked for is done.
func (s *Scrolls) Settle() { s.follow = false }

// Reset starts a view, which marks every scroll it draws again.
func (s *Scrolls) Reset() {
	s.marks.Reset()
	s.focused = ""
	for _, st := range s.state {
		st.drawn = false
	}
}

// Wheel scrolls the smallest scroll drawn under the wheel that moves along
// its axis, three lines or columns a notch, and reports whether one moved.
func (s *Scrolls) Wheel(msg tea.MouseWheelMsg) bool {
	dx, dy := 0, 0
	switch msg.Button {
	case tea.MouseWheelUp:
		dy = -3
	case tea.MouseWheelDown:
		dy = 3
	case tea.MouseWheelLeft:
		dx = -3
	case tea.MouseWheelRight:
		dx = 3
	}
	if msg.Mod.Contains(tea.ModShift) {
		dx, dy = dy, dx
	}
	key, ok := s.marks.AtWhere(msg.X, msg.Y, func(key string) bool {
		st := s.state[key]
		return st != nil && (dy != 0 && st.axis != ScrollHorizontal || dx != 0 && st.axis != ScrollVertical)
	})
	if !ok {
		return false
	}
	return s.move(key, dx, dy)
}

// Key pages the scroll focus was drawn in -- PgUp, PgDn, Home and End --
// and reports whether it moved.
func (s *Scrolls) Key(msg tea.KeyPressMsg) bool {
	st := s.state[s.focused]
	if st == nil || st.axis == ScrollHorizontal {
		return false
	}
	page := max(st.viewH-1, 1)
	switch msg.Code {
	case tea.KeyPgUp:
		return s.move(s.focused, 0, -page)
	case tea.KeyPgDown:
		return s.move(s.focused, 0, page)
	}
	return false
}

func (s *Scrolls) move(key string, dx, dy int) bool {
	st := s.state[key]
	x, y := st.x, st.y
	st.x += dx
	st.y += dy
	st.clamp()
	st.due = st.atEnd()
	if !st.due {
		st.reached = -1
	}
	return st.x != x || st.y != y
}

// Due reports whether the scroll key names has its end in view at a content
// size it was not yet reported at, and takes it as reported: `@reachEnd`
// fires once as the end arrives and again as the content changes size while
// it is there.
func (s *Scrolls) Due(key string) bool {
	st := s.state[key]
	if st == nil || !st.waiting() {
		return false
	}
	st.reached = st.size()
	return true
}

func (st *scrollState) size() int {
	if st.axis == ScrollHorizontal {
		return st.contentW
	}
	return st.contentH
}

func (st *scrollState) waiting() bool {
	return st.drawn && st.reports && st.due && st.reached != st.size()
}

// Waiting reports whether a scroll has its end in view at a size not yet
// reported, which the view wakes Update to report.
func (s *Scrolls) Waiting() bool {
	for _, st := range s.state {
		if st.waiting() {
			return true
		}
	}
	return false
}
