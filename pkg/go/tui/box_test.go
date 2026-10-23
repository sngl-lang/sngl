package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// A part that draws nothing takes no line, and a gap is that many blank
// lines between two parts that do, padded to the column's width as every
// line of it is.
func TestColumnLeavesOutWhatDrawsNothing(t *testing.T) {
	if got, want := Column(0, "a", "", "", "b"), "a\nb"; got != want {
		t.Errorf("Column(0) = %q, want %q", got, want)
	}
	if got, want := Column(1, "a", "", "b"), "a\n \nb"; got != want {
		t.Errorf("Column(1) = %q, want %q", got, want)
	}
	if got, want := Column(2, "a", "b"), "a\n \n \nb"; got != want {
		t.Errorf("Column(2) = %q, want %q", got, want)
	}
}

// Two columns of a row are a gap apart: badges side by side were written
// with no space between them.
func TestRowPutsTheGapBetweenParts(t *testing.T) {
	if got, want := Row(1, "Grass", "", "Poison"), "Grass Poison"; got != want {
		t.Errorf("Row(1) = %q, want %q", got, want)
	}
	if got, want := Row(0, "a", "b"), "ab"; got != want {
		t.Errorf("Row(0) = %q, want %q", got, want)
	}
}

// A flexing part takes what the fixed ones and the gaps leave of the row's
// width, so two rows with different text in it line their next columns up.
func TestFlexRowLinesColumnsUp(t *testing.T) {
	one := FlexRow(20, 1, []string{"01", "Tackle", "Normal"}, []float64{0, 1, 0})
	two := FlexRow(20, 1, []string{"12", "Razor Leaf", "Poison"}, []float64{0, 1, 0})
	if want := "01 Tackle     Normal"; one != want {
		t.Errorf("first row %q, want %q", one, want)
	}
	if want := "12 Razor Leaf Poison"; two != want {
		t.Errorf("second row %q, want %q", two, want)
	}
	// A part never shrinks below its own width, and a row with no width is
	// a plain row.
	if got, want := FlexRow(5, 1, []string{"long text", "x"}, []float64{1, 0}), "long text x"; got != want {
		t.Errorf("overfull row %q, want %q", got, want)
	}
	if got, want := FlexRow(0, 1, []string{"a", "b"}, []float64{1, 0}), "a b"; got != want {
		t.Errorf("row with no width %q, want %q", got, want)
	}
}

// A box's alignItems puts each part across it: a column's names centered
// under the widest, a row's short part centered down the tallest. Joined at
// the start, a card's names sat at its left.
func TestAlignedPartsSitAcross(t *testing.T) {
	if got, want := ColumnAt(lipgloss.Center, 0, "Fig", "a long name"), "    Fig    \na long name"; got != want {
		t.Errorf("ColumnAt(Center) = %q, want %q", got, want)
	}
	if got, want := ColumnAt(lipgloss.Right, 0, "ab", "abcd"), "  ab\nabcd"; got != want {
		t.Errorf("ColumnAt(Right) = %q, want %q", got, want)
	}
	if got, want := RowAt(lipgloss.Center, 1, "a\nb\nc", "x"), "a  \nb x\nc  "; got != want {
		t.Errorf("RowAt(Center) = %q, want %q", got, want)
	}
	if got, want := FlexRowAt(lipgloss.Bottom, 0, 1, []string{"a\nb", "x"}, []float64{1, 0}), "a  \nb x"; got != want {
		t.Errorf("FlexRowAt(Bottom) = %q, want %q", got, want)
	}
}
