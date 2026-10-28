package tui

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// DashedBorder is a single-line border drawn in dashes, which lipgloss does
// not ship.
func DashedBorder() lipgloss.Border {
	b := lipgloss.NormalBorder()
	b.Top, b.Bottom, b.Left, b.Right = "╌", "╌", "╎", "╎"
	return b
}

// Part is one child of a box joined along an axis: rendered already, or
// rendered once the box knows how many cells of its free space it grows into.
type Part struct {
	grow   float64
	text   string
	render func(cells int) string
}

func Fixed(text string) Part { return Part{text: text} }

// Grow is a part taking grow shares of the free space. Given none, it renders
// at its own size.
func Grow(grow float64, render func(cells int) string) Part {
	if grow <= 0 {
		return Part{text: render(0)}
	}
	return Part{grow: grow, render: render}
}

// Join lays parts out along an axis, gap cells apart, and hands each growing
// part its share of what the fixed ones leave of avail cells. An avail of zero
// is a box whose extent nothing knows, where each part takes its own. Where
// nothing grows, justify places the parts in what they leave, as CSS's
// justify-content: 0 at the start, 1 centered, 2 at the end, 3 spaced between,
// 4 around and 5 evenly.
func Join(horizontal bool, align lipgloss.Position, justify int, gap int, avail int, parts []Part) string {
	size := lipgloss.Height
	if horizontal {
		size = lipgloss.Width
	}
	// A part that draws nothing -- a request, a branch not taken -- takes no
	// room and no gap, as an empty box takes none in CSS.
	parts = slices.DeleteFunc(slices.Clone(parts), func(p Part) bool { return p.render == nil && p.text == "" })
	free, shares := avail, 0.0
	for _, p := range parts {
		if p.render == nil {
			free -= size(p.text)
		} else {
			shares += p.grow
		}
	}
	if len(parts) > 1 {
		free -= gap * (len(parts) - 1)
	}
	grows := shares > 0
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		text := p.text
		if p.render != nil {
			cells := 0
			if free > 0 {
				// The last grower takes what rounding left, so the shares fill
				// the box exactly.
				cells = int(float64(free)*p.grow/shares + 0.5)
				free -= cells
				shares -= p.grow
			}
			text = p.render(cells)
		}
		texts = append(texts, text)
	}
	if grows || free <= 0 || justify == 0 || len(texts) == 0 {
		return joinSpaced(horizontal, align, gap, texts)
	}
	return joinJustified(horizontal, align, justify, gap, free, texts)
}

// joinJustified joins texts with free cells placed as justify says: before
// them, between them, around each or evenly.
func joinJustified(horizontal bool, align lipgloss.Position, justify, gap, free int, texts []string) string {
	n := len(texts)
	lead, between, extra := 0, 0, 0
	switch justify {
	case 1:
		lead = free / 2
	case 2:
		lead = free
	case 3:
		if n == 1 {
			break
		}
		between, extra = free/(n-1), free%(n-1)
	case 4:
		lead = free / (2 * n)
		between = 2 * lead
	case 5:
		lead = free / (n + 1)
		between = lead
	}
	blank := func(cells int) string {
		if horizontal {
			return strings.Repeat(" ", cells)
		}
		return strings.Repeat("\n", cells-1)
	}
	out := make([]string, 0, 2*n+1)
	if lead > 0 {
		out = append(out, blank(lead))
	}
	for i, t := range texts {
		if i > 0 {
			// The first gaps take what dividing the free space left over.
			cells := gap + between
			if i <= extra {
				cells++
			}
			if cells > 0 {
				out = append(out, blank(cells))
			}
		}
		out = append(out, t)
	}
	if horizontal {
		return lipgloss.JoinHorizontal(align, out...)
	}
	return lipgloss.JoinVertical(align, out...)
}

// Spaced joins texts along an axis, gap cells apart.
func Spaced(horizontal bool, align lipgloss.Position, gap int, texts ...string) string {
	return joinSpaced(horizontal, align, gap, texts)
}

func joinSpaced(horizontal bool, align lipgloss.Position, gap int, texts []string) string {
	// Joined, an empty text was a blank line where nothing was drawn: the
	// panels of the tabs not shown, a loop's copies that render a request.
	texts = slices.DeleteFunc(slices.Clone(texts), func(t string) bool { return t == "" })
	if gap > 0 && len(texts) > 1 {
		spacer := strings.Repeat("\n", gap-1)
		if horizontal {
			spacer = strings.Repeat(" ", gap)
		}
		spaced := make([]string, 0, 2*len(texts)-1)
		for i, t := range texts {
			if i > 0 {
				spaced = append(spaced, spacer)
			}
			spaced = append(spaced, t)
		}
		texts = spaced
	}
	if horizontal {
		return lipgloss.JoinHorizontal(align, texts...)
	}
	return lipgloss.JoinVertical(align, texts...)
}

// Avail is the width a terminal gives the view: the one it reported, or the
// conventional 80 columns where it has said none -- a test's snapshot, a first
// frame.
func Avail(width int) int {
	if width <= 0 {
		return 80
	}
	return width
}

// PictureBox is the box, in cells, a picture shown in look is fitted into:
// the width and height inside look's frame where it fixes them, else cols by
// rows.
func PictureBox(look lipgloss.Style, cols, rows int) (int, int) {
	if w := look.GetWidth(); w > 0 {
		cols = max(1, w-(look.GetHorizontalFrameSize()-look.GetHorizontalMargins()))
	}
	if h := look.GetHeight(); h > 0 {
		rows = max(1, h-(look.GetVerticalFrameSize()-look.GetVerticalMargins()))
	}
	return cols, rows
}

// Inner is the extent inside look's frame of a box placed in w by h cells, or
// in the size look fixes; zero where neither is known.
func Inner(look lipgloss.Style, w, h int) (int, int) {
	inner := func(fixed, avail, margins, frame int) int {
		switch {
		case fixed > 0:
			return max(0, fixed-(frame-margins))
		case avail > 0:
			return max(0, avail-frame)
		}
		return 0
	}
	return inner(look.GetWidth(), w, look.GetHorizontalMargins(), look.GetHorizontalFrameSize()),
		inner(look.GetHeight(), h, look.GetVerticalMargins(), look.GetVerticalFrameSize())
}

// Sized is look filling cells along an axis, margins included; look itself
// where cells is zero.
func Sized(look lipgloss.Style, horizontal bool, cells int) lipgloss.Style {
	if cells <= 0 {
		return look
	}
	if horizontal {
		return look.Width(max(1, cells-look.GetHorizontalMargins()))
	}
	return look.Height(max(1, cells-look.GetVerticalMargins()))
}
