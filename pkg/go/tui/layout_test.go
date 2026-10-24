package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// A part that draws nothing takes no line, and a gap is that many blank
// lines between two parts that do, padded to the column's width as every
// line of it is.
func TestSpacedLeavesOutWhatDrawsNothing(t *testing.T) {
	if got, want := Spaced(false, lipgloss.Left, 0, "a", "", "", "b"), "a\nb"; got != want {
		t.Errorf("Spaced(0) = %q, want %q", got, want)
	}
	if got, want := Spaced(false, lipgloss.Left, 1, "a", "", "b"), "a\n \nb"; got != want {
		t.Errorf("Spaced(1) = %q, want %q", got, want)
	}
	if got, want := Spaced(true, lipgloss.Top, 1, "Grass", "", "Poison"), "Grass Poison"; got != want {
		t.Errorf("a row's Spaced(1) = %q, want %q", got, want)
	}
}

// A growing part takes what the fixed ones and the gaps leave of the row's
// width, so two rows with different text in it line their next columns up;
// an empty fixed part takes neither room nor a gap.
func TestJoinLinesColumnsUp(t *testing.T) {
	row := func(count, name, kind string) string {
		grow := Grow(1, func(cells int) string { return lipgloss.NewStyle().Width(cells).Render(name) })
		return Join(true, lipgloss.Top, 1, 20, []Part{Fixed(count), Fixed(""), grow, Fixed(kind)})
	}
	if got, want := row("01", "Tackle", "Normal"), "01 Tackle     Normal"; got != want {
		t.Errorf("first row %q, want %q", got, want)
	}
	if got, want := row("12", "Razor Leaf", "Poison"), "12 Razor Leaf Poison"; got != want {
		t.Errorf("second row %q, want %q", got, want)
	}
}

// A picture fills the width and height its box's look fixes, inside the
// box's frame, and is the default box where the look fixes neither.
func TestPictureBoxIsTheLooksInside(t *testing.T) {
	cols, rows := PictureBox(lipgloss.NewStyle().Width(10).Height(4).Padding(0, 1), 24, 12)
	if cols != 8 || rows != 4 {
		t.Errorf("PictureBox = %dx%d, want 8x4", cols, rows)
	}
	if cols, rows := PictureBox(lipgloss.NewStyle(), 24, 12); cols != 24 || rows != 12 {
		t.Errorf("PictureBox of no size = %dx%d, want the default 24x12", cols, rows)
	}
}
