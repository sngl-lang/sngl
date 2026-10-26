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
		return Join(true, lipgloss.Top, 0, 1, 20, []Part{Fixed(count), Fixed(""), grow, Fixed(kind)})
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

// Where nothing grows, justify places the parts in what they and the gaps
// leave of the extent, as CSS's justify-content; a column's free space is
// blank lines.
func TestJoinJustifies(t *testing.T) {
	parts := []Part{Fixed("a"), Fixed("b")}
	for _, c := range []struct {
		justify int
		want    string
	}{
		{0, "a b"},
		{1, "    a b"},
		{2, "        a b"},
		{3, "a         b"},
		{4, "  a     b"},
		{5, "  a   b"},
	} {
		if got := Join(true, lipgloss.Top, c.justify, 1, 11, parts); got != c.want {
			t.Errorf("justify %d = %q, want %q", c.justify, got, c.want)
		}
	}
	if got, want := Join(false, lipgloss.Left, 2, 0, 4, parts), " \n \na\nb"; got != want {
		t.Errorf("a column's end = %q, want %q", got, want)
	}
}
