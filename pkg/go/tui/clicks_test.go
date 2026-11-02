package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// A block is found where the layout put it: two bordered keys side by side
// under a line of text, each its own box, and the text between none.
func TestClicksFindTheBlockUnderAClick(t *testing.T) {
	var c Clicks[string]
	key := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Background(lipgloss.Color("#333333")).Padding(0, 1)
	row := lipgloss.JoinHorizontal(lipgloss.Top, c.Mark("7", key.Render("7")), " ", c.Mark("8", key.Render("8")))
	view := c.Scan(lipgloss.JoinVertical(lipgloss.Left, "readout", row), 0, 0)
	if got, want := lipgloss.Width(view), 11; got != want {
		t.Fatalf("width %d, want %d:\n%s", got, want, view)
	}
	for _, tc := range []struct {
		x, y int
		want string
	}{
		{0, 1, "7"}, {4, 3, "7"}, {2, 2, "7"},
		{6, 1, "8"}, {10, 3, "8"},
		{5, 2, ""}, {2, 0, ""}, {2, 4, ""},
	} {
		got, ok := c.At(tc.x, tc.y)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("At(%d, %d) = %q, %v; want %q", tc.x, tc.y, got, ok, tc.want)
		}
	}
}

// A view is scanned afresh each time: what the last one marked is gone, and
// an overlay's box covers what was found beneath it.
func TestClicksResetAndCover(t *testing.T) {
	var c Clicks[int]
	c.Scan(c.Mark(1, "one"), 0, 0)
	c.Reset()
	if _, ok := c.At(0, 0); ok {
		t.Fatal("a reset Clicks found a block")
	}
	under := c.Mark(1, "under")
	box := c.Mark(2, "box")
	c.Scan(under, 0, 0)
	c.Cover()
	c.Scan(box, 1, 0)
	if _, ok := c.At(0, 0); ok {
		t.Error("a covered block took a click")
	}
	if got, _ := c.At(2, 0); got != 2 {
		t.Errorf("At(2, 0) = %d, want the overlay's 2", got)
	}
}

// A block inside another is pressed where both hold the click, and a mark
// whose end was cut away marks nothing.
func TestClicksInnermostAndCutMarks(t *testing.T) {
	var c Clicks[string]
	inner := c.Mark("inner", "[x]")
	outer := c.Mark("outer", "card "+inner+" end")
	cut := c.Mark("cut", "gone")
	cut = cut[:len(cut)-4]
	view := c.Scan(outer+"\n"+cut, 0, 0)
	if want := "card [x] end\ngone"; view != want {
		t.Fatalf("scanned %q, want %q", view, want)
	}
	if got, _ := c.At(6, 0); got != "inner" {
		t.Errorf("At(6, 0) = %q, want inner", got)
	}
	if got, _ := c.At(0, 0); got != "outer" {
		t.Errorf("At(0, 0) = %q, want outer", got)
	}
	if got, ok := c.At(0, 1); ok {
		t.Errorf("At(0, 1) = %q, want nothing", got)
	}
}
