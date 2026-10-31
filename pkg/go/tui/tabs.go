package tui

import "charm.land/lipgloss/v2"

// TabLook is the box one tab of a `ui.tabs` is drawn in. An underline bar is
// boxes whose bottom edges are the top of the panel's box: the selected one's
// is open onto its panel and the rest closed, and the first's left corner is
// the panel's. A pills bar is labels, the selected one reversed.
func TabLook(selected, first, disabled bool, variant string) lipgloss.Style {
	look := lipgloss.NewStyle().Padding(0, 1).Faint(disabled)
	if variant == "pills" {
		return look.Reverse(selected)
	}
	b := lipgloss.RoundedBorder()
	switch {
	case selected && first:
		b.Bottom, b.BottomLeft, b.BottomRight = " ", "│", "└"
	case selected:
		b.Bottom, b.BottomLeft, b.BottomRight = " ", "┘", "└"
	case first:
		b.BottomLeft, b.BottomRight = "├", "┴"
	default:
		b.BottomLeft, b.BottomRight = "┴", "┴"
	}
	return look.Border(b)
}

// TabGap carries an underline bar's bottom edge on to the panel's right edge,
// rendering two empty lines where the tabs have a top and a label.
func TabGap() lipgloss.Style {
	b := lipgloss.Border{Bottom: "─", Right: " ", BottomRight: "╮"}
	return lipgloss.NewStyle().Border(b, false, true, true, false)
}

// TabPanel is the box the panels are drawn in under an underline bar, which
// draws its top edge; under pills, none.
func TabPanel(variant string) lipgloss.Style {
	if variant == "pills" {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), false, true, true, true)
}
