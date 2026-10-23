package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Column joins parts top to bottom, gap blank lines between two of them. A
// part that is empty -- a branch not taken, a request, an effect -- is left
// out, as a box with nothing in it takes no room in CSS: joined, it was a
// blank line where nothing was drawn.
func Column(gap int, parts ...string) string {
	return ColumnAt(lipgloss.Left, gap, parts...)
}

// ColumnAt is Column with each part at across the widest one: a box's
// `alignItems`, lipgloss.Center for center and lipgloss.Right for end.
func ColumnAt(at lipgloss.Position, gap int, parts ...string) string {
	parts = drawn(parts)
	if gap > 0 && len(parts) > 1 {
		parts = spaced(parts, strings.Repeat("\n", gap-1))
	}
	return lipgloss.JoinVertical(at, parts...)
}

// Row joins parts left to right, gap columns between two of them, leaving
// out the empty ones.
func Row(gap int, parts ...string) string {
	return RowAt(lipgloss.Top, gap, parts...)
}

// RowAt is Row with each part at down the tallest one: lipgloss.Center for
// center and lipgloss.Bottom for end.
func RowAt(at lipgloss.Position, gap int, parts ...string) string {
	parts = drawn(parts)
	if gap > 0 && len(parts) > 1 {
		parts = spaced(parts, strings.Repeat(" ", gap))
	}
	return lipgloss.JoinHorizontal(at, parts...)
}

// FlexRow is Row for a row some of whose parts ask for a share of it: weights
// holds each part's, zero for a part that keeps its own width. The parts with
// a weight share what width leaves after the others and the gaps, each at
// least its own width, so the rows of a table whose columns are fixed but
// one line their columns up. A row given no width is Row.
func FlexRow(width, gap int, parts []string, weights []float64) string {
	return FlexRowAt(lipgloss.Top, width, gap, parts, weights)
}

// FlexRowAt is FlexRow with each part at down the tallest one, as RowAt.
func FlexRowAt(at lipgloss.Position, width, gap int, parts []string, weights []float64) string {
	var kept []string
	var shares []float64
	total := 0.0
	for i, p := range parts {
		if p == "" {
			continue
		}
		w := 0.0
		if i < len(weights) {
			w = weights[i]
		}
		kept = append(kept, p)
		shares = append(shares, w)
		total += w
	}
	if width <= 0 || total <= 0 {
		return RowAt(at, gap, kept...)
	}
	free := width - gap*max(len(kept)-1, 0)
	for i, p := range kept {
		if shares[i] == 0 {
			free -= lipgloss.Width(p)
		}
	}
	free = max(free, 0)
	for i, p := range kept {
		if shares[i] == 0 {
			continue
		}
		if w := int(float64(free) * shares[i] / total); w > lipgloss.Width(p) {
			kept[i] = lipgloss.NewStyle().Width(w).Render(p)
		}
	}
	return RowAt(at, gap, kept...)
}

func drawn(parts []string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// spaced puts sep between every two parts.
func spaced(parts []string, sep string) []string {
	out := make([]string, 0, len(parts)*2-1)
	for i, p := range parts {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, p)
	}
	return out
}
