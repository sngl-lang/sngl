package tui

import "charm.land/lipgloss/v2"

// Focused is look as the stop focus is on is drawn: its edge bold, where it
// has one. A focused stop is also marked `>` at its start, which the view
// writes; the bold edge is what a select, a button and a tab share with it.
//
// A corner where the box meets a line it sits on -- a tab's bottom corners,
// joining the panel's top edge -- turns bold only up the box's side, the line
// it joins staying light.
func Focused(look lipgloss.Style, focused bool) lipgloss.Style {
	b, top, right, bottom, left := look.GetBorder()
	if !focused || !(top || right || bottom || left) {
		return look
	}
	bold := func(s string, heavy map[string]string) string {
		if h, ok := heavy[s]; ok {
			return h
		}
		return s
	}
	b.Top = bold(b.Top, edges)
	b.Bottom = bold(b.Bottom, edges)
	b.Left = bold(b.Left, edges)
	b.Right = bold(b.Right, edges)
	b.TopLeft = bold(b.TopLeft, topLeft)
	b.TopRight = bold(b.TopRight, topRight)
	b.BottomLeft = bold(b.BottomLeft, bottomLeft)
	b.BottomRight = bold(b.BottomRight, bottomRight)
	return look.Border(b, top, right, bottom, left)
}

var (
	edges       = map[string]string{"─": "━", "│": "┃", "╌": "╍", "╎": "╏"}
	topLeft     = map[string]string{"╭": "┏", "┌": "┏"}
	topRight    = map[string]string{"╮": "┓", "┐": "┓"}
	bottomLeft  = map[string]string{"╰": "┗", "└": "┗", "├": "┞", "┴": "┸", "┘": "┚", "│": "╿"}
	bottomRight = map[string]string{"╯": "┛", "┘": "┛", "┴": "┸", "└": "┖"}
)

// ButtonLook is the box a button is drawn in: look, given a rounded edge
// where it draws none, which a focused button draws bold (Focused).
func ButtonLook(look lipgloss.Style) lipgloss.Style {
	if _, top, right, bottom, left := look.GetBorder(); top || right || bottom || left {
		return look
	}
	return look.Border(lipgloss.RoundedBorder()).PaddingRight(1)
}

// CardLook is the box a card is drawn in: look, given the dark surface of
// Material's filled card where it paints no background of its own, as an
// Android card is.
func CardLook(look lipgloss.Style) lipgloss.Style {
	if _, none := look.GetBackground().(lipgloss.NoColor); !none {
		return look
	}
	return look.Background(lipgloss.Color("#2B2930")).PaddingRight(1)
}
