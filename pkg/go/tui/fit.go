package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Fit is what a box does with text that does not fit it, and whether it is
// drawn at all: what a lipgloss.Style cannot say. lib/platform/bubbletea's
// fitOf builds one from a ui.Style, and the zero Fit is lipgloss's own
// rendering.
type Fit struct {
	// Wrap is 0 to break lines between words at the box's width, lipgloss's
	// own; 1 for one line, its newlines spaces, CSS's nowrap; 2 for the lines
	// written, none broken, CSS's pre. A line the box is too narrow for is cut
	// at its edge, a terminal having nothing beyond the box to draw it on.
	Wrap int
	// Hidden draws nothing.
	Hidden bool
}

// Render is look.Render(text) as fit says.
func Render(look lipgloss.Style, fit Fit, text string) string {
	if fit.Hidden {
		return ""
	}
	if fit.Wrap == 1 || fit.Wrap == 2 {
		if fit.Wrap == 1 {
			text = strings.ReplaceAll(text, "\n", " ")
		}
		if w, _ := Inner(look, 0, 0); w > 0 {
			lines := strings.Split(text, "\n")
			for i, l := range lines {
				lines[i] = ansi.Truncate(l, w, "")
			}
			text = strings.Join(lines, "\n")
		}
	}
	return look.Render(text)
}
