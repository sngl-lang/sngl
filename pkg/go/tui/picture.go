package tui

import (
	"strings"
	"sync"

	"duckfam.us/sngl/pkg/go/snglimage"
)

// pictureAt is a picture drawn at a size.
type pictureAt struct {
	url        string
	cols, rows int
}

// pictures holds each picture as Picture last drew it, since a view draws
// every one again on every message and a picture at hand does not change.
// It is a cache, emptied whole once it holds more than maxPictures.
var (
	picturesMu sync.Mutex
	pictures   = map[pictureAt]string{}
)

const maxPictures = 2048

// Picture is the picture url names in half-block cells, fitted into cols by
// rows keeping its shape: a terminal has no size a pixel has, so the box is
// the program's to choose. One still on its way, or one that cannot be read,
// is blank cells of that size.
func Picture(url string, cols, rows int) string {
	at := pictureAt{url, cols, rows}
	picturesMu.Lock()
	s, ok := pictures[at]
	picturesMu.Unlock()
	if ok {
		return s
	}
	s, done := picture(url, cols, rows)
	if done {
		picturesMu.Lock()
		if len(pictures) >= maxPictures {
			clear(pictures)
		}
		pictures[at] = s
		picturesMu.Unlock()
	}
	return s
}

// picture is Picture drawn afresh, and whether it is the picture for good: one
// still on its way is not.
func picture(url string, cols, rows int) (string, bool) {
	img, err := snglimage.Load(url)
	if err != nil {
		blank := strings.Repeat(" ", cols)
		lines := make([]string, rows)
		for i := range lines {
			lines[i] = blank
		}
		return strings.Join(lines, "\n"), err != snglimage.ErrPending
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
	return halfBlock(img, cols, rows), true
}
