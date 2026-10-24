package tui

import (
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
// is a box whose extent nothing knows, where each part takes its own.
func Join(horizontal bool, align lipgloss.Position, gap int, avail int, parts []Part) string {
	size := lipgloss.Height
	if horizontal {
		size = lipgloss.Width
	}
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
	return joinSpaced(horizontal, align, gap, texts)
}

// Spaced joins texts along an axis, gap cells apart.
func Spaced(horizontal bool, align lipgloss.Position, gap int, texts ...string) string {
	return joinSpaced(horizontal, align, gap, texts)
}

func joinSpaced(horizontal bool, align lipgloss.Position, gap int, texts []string) string {
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
