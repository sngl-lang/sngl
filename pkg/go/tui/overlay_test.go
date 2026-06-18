package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestOverlayCenterEmptyBoxNoOp(t *testing.T) {
	base := "line one\nline two\nline three"
	if got := OverlayCenter(base, "", 80, 24, true); got != base {
		t.Errorf("empty box should return base unchanged; got %q", got)
	}
}

func TestOverlaySideEmptyBoxNoOp(t *testing.T) {
	base := "alpha\nbeta\ngamma"
	if got := OverlaySide(base, "", "right", 80, 24); got != base {
		t.Errorf("empty box should return base unchanged; got %q", got)
	}
}

func TestOverlayCenterDimensions(t *testing.T) {
	// A 10x5 base, a small box, composited on a 40x12 terminal.
	base := strings.TrimRight(strings.Repeat("..........\n", 5), "\n")
	box := "[ MODAL ]"
	out := OverlayCenter(base, box, 40, 12, false)

	// Trailing blank cells are trimmed per line, so height (not width) is the
	// stable dimension to assert.
	if h := lipgloss.Height(out); h != 12 {
		t.Errorf("composited height = %d, want 12", h)
	}
	if !strings.Contains(out, "MODAL") {
		t.Errorf("composited output should contain the box text; got:\n%s", out)
	}
	if !strings.Contains(out, "..........") {
		t.Errorf("composited output should still show base content; got:\n%s", out)
	}
}

func TestOverlayCenterGrowsToFitBase(t *testing.T) {
	// Zero terminal size: must fall back and still fit a base larger than the
	// 80x24 default.
	base := strings.TrimRight(strings.Repeat("x\n", 40), "\n") // 40 lines tall
	out := OverlayCenter(base, "box", 0, 0, false)
	if h := lipgloss.Height(out); h < 40 {
		t.Errorf("composited height = %d, want >= 40 (base must not clip)", h)
	}
}

func TestOverlaySidePlacement(t *testing.T) {
	base := strings.TrimRight(strings.Repeat(strings.Repeat(".", 20)+"\n", 6), "\n")
	box := "DRAWER"

	for _, side := range []string{"left", "right", "top", "bottom"} {
		out := OverlaySide(base, box, side, 20, 6)
		if h := lipgloss.Height(out); h != 6 {
			t.Errorf("side %q: height = %d, want 6", side, h)
		}
		if !strings.Contains(out, "DRAWER") {
			t.Errorf("side %q: output should contain box text; got:\n%s", side, out)
		}
	}
}

func TestOverlaySideRightAnchors(t *testing.T) {
	base := strings.TrimRight(strings.Repeat(strings.Repeat(".", 20)+"\n", 3), "\n")
	box := "RT"
	out := OverlaySide(base, box, "right", 20, 3)
	lines := strings.Split(out, "\n")
	// The box "RT" (width 2) anchored right should land at the far columns of
	// the first row; the leftmost columns stay base dots.
	if !strings.HasPrefix(lines[0], ".") {
		t.Errorf("right-anchored box should leave left edge as base; row0=%q", lines[0])
	}
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "RT") {
		t.Errorf("right-anchored box should land at the right edge; row0=%q", lines[0])
	}
}
