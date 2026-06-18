package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// overlayDims resolves the canvas dimensions used to composite an overlay over
// base. It prefers the supplied terminal width/height (from tea.WindowSizeMsg)
// but falls back to a sane default and always grows to fit the base content so
// the background is never clipped when the terminal size is unknown (zero).
func overlayDims(base string, w, h int) (int, int) {
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	if bw := lipgloss.Width(base); bw > w {
		w = bw
	}
	if bh := lipgloss.Height(base); bh > h {
		h = bh
	}
	return w, h
}

// composite draws box at (x, y) on top of base within a w×h canvas and returns
// the rendered string. The box layer sits above the base layer (higher Z), so
// its cells overwrite the background where it lands; base shows through
// everywhere else.
func composite(base, box string, x, y, w, h int) string {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	// Pad base to the full terminal extent so the box can land anywhere over a
	// well-defined area (Place left/top-aligns and fills the remainder with
	// spaces). The Compositor honors each layer's X/Y/Z offsets — Canvas.Compose
	// alone draws every layer at the origin and ignores them. Trailing blank
	// cells are trimmed from each rendered line, which is the desired terminal
	// output.
	base = lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, base)
	baseLayer := lipgloss.NewLayer(base).X(0).Y(0).Z(0)
	boxLayer := lipgloss.NewLayer(box).X(x).Y(y).Z(1)
	return lipgloss.NewCompositor(baseLayer, boxLayer).Render()
}

// dimBase applies a faint style to every line of base so a modal/drawer box
// composited on top reads as the focused layer and the background recedes.
func dimBase(base string) string {
	faint := lipgloss.NewStyle().Faint(true)
	lines := strings.Split(base, "\n")
	for i, ln := range lines {
		lines[i] = faint.Render(ln)
	}
	return strings.Join(lines, "\n")
}

// OverlayCenter composites box centered over base within a w×h terminal. When
// box is empty (the overlay is closed) it returns base unchanged — this is how
// the `if open { Overlay(...) }` gating no-ops while closed. When dim is true
// the background is faded before compositing so the modal stands out.
func OverlayCenter(base, box string, w, h int, dim bool) string {
	if box == "" {
		return base
	}
	cw, ch := overlayDims(base, w, h)
	bg := base
	if dim {
		bg = dimBase(base)
	}
	bw := lipgloss.Width(box)
	bh := lipgloss.Height(box)
	x := (cw - bw) / 2
	y := (ch - bh) / 2
	return composite(bg, box, x, y, cw, ch)
}

// OverlaySide composites box anchored to the named edge (left/right/top/bottom)
// of a w×h terminal over base. When box is empty it returns base unchanged
// (closed-drawer no-op). The drawer covers its strip; base shows through the
// rest. An unknown side defaults to "right".
func OverlaySide(base, box, side string, w, h int) string {
	if box == "" {
		return base
	}
	cw, ch := overlayDims(base, w, h)
	bw := lipgloss.Width(box)
	bh := lipgloss.Height(box)

	var x, y int
	switch side {
	case "left":
		x, y = 0, 0
	case "top":
		x, y = 0, 0
	case "bottom":
		x, y = 0, ch-bh
	case "right":
		x, y = cw-bw, 0
	default:
		x, y = cw-bw, 0
	}
	return composite(base, box, x, y, cw, ch)
}
