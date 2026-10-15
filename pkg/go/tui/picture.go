package tui

import (
	"strings"

	"duckfam.us/sngl/pkg/go/snglimage"
)

// Picture is the picture url names in half-block cells, fitted into cols by
// rows keeping its shape: a terminal has no size a pixel has, so the box is
// the program's to choose. One still on its way, or one that cannot be read,
// is blank cells of that size.
func Picture(url string, cols, rows int) string {
	img, err := snglimage.Load(url)
	if err != nil {
		blank := strings.Repeat(" ", cols)
		lines := make([]string, rows)
		for i := range lines {
			lines[i] = blank
		}
		return strings.Join(lines, "\n")
	}
	b := img.Bounds()
	if b.Dx() > 0 && b.Dy() > 0 {
		// A cell is one pixel wide and two tall.
		if fitRows := cols * b.Dy() / b.Dx() / 2; fitRows < rows {
			rows = max(fitRows, 1)
		} else {
			cols = max(rows*2*b.Dx()/b.Dy(), 1)
		}
	}
	return halfBlock(img, cols, rows)
}
