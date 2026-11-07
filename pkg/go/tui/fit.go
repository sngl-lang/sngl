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
	return Paint(look, text)
}

// Within is look, or look as wide as avail cells where text rendered in it
// would be wider: a text breaks its lines at the width of the box it is in,
// as CSS's does, where it would otherwise run past it and the box around it
// would break the lines it had padded. A look fixing its own width, and an
// avail of zero, a box whose extent nothing knows, leave it as it is.
func Within(look lipgloss.Style, avail int, text string) lipgloss.Style {
	if avail <= 0 || look.GetWidth() > 0 || lipgloss.Width(look.Render(text)) <= avail {
		return look
	}
	return Sized(look, true, avail)
}

// Paint is look.Render(text) with look's background under all of text: a
// block styled inside it ends in a reset, which ended the background too, so
// a card's color stopped after its first name. Each reset in text is followed
// by the background again.
func Paint(look lipgloss.Style, text string) string {
	if _, none := look.GetBackground().(lipgloss.NoColor); none || look.GetBackground() == nil {
		return look.Render(text)
	}
	marked := lipgloss.NewStyle().Background(look.GetBackground()).Render("x")
	bg := marked[:strings.Index(marked, "x")]
	if bg != "" {
		text = strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+bg)
		text = strings.ReplaceAll(text, "\x1b[m", "\x1b[m"+bg)
	}
	return look.Render(text)
}
