// Package tui renders a 2D image into terminal output. It is framework-
// agnostic (any Go TUI can use it). RenderTerminal emits kitty graphics
// escapes when the terminal supports them, otherwise a truecolor Unicode
// half-block grid.
package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"sync"
)

// RenderTerminal renders img to a terminal string sized to cols x rows cells.
// id is a per-image identifier (>0) used by the kitty path to distinguish
// multiple images on screen; it is ignored by the half-block fallback. Callers
// rendering several images in one frame must pass distinct ids.
func RenderTerminal(img image.Image, cols, rows, id int) string {
	if kittySupported() {
		return kitty(img, cols, rows, id)
	}
	return halfBlock(img, cols, rows)
}

// sampleAt nearest-neighbor samples img at the given fraction (0..1).
func sampleAt(img image.Image, fx, fy float64) (r, g, b, a uint8) {
	b2 := img.Bounds()
	x := b2.Min.X + int(fx*float64(b2.Dx()))
	y := b2.Min.Y + int(fy*float64(b2.Dy()))
	if x >= b2.Max.X {
		x = b2.Max.X - 1
	}
	if y >= b2.Max.Y {
		y = b2.Max.Y - 1
	}
	cr, cg, cb, ca := img.At(x, y).RGBA()
	return uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), uint8(ca >> 8)
}

// halfBlock downsamples img to cols x rows cells. Each cell is ▀ with
// foreground = the top sub-pixel and background = the bottom sub-pixel, so one
// character cell shows two vertically-stacked colored pixels.
func halfBlock(img image.Image, cols, rows int) string {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			fx := (float64(col) + 0.5) / float64(cols)
			topFy := (float64(row*2) + 0.5) / float64(rows*2)
			botFy := (float64(row*2+1) + 0.5) / float64(rows*2)
			tr, tg, tb, _ := sampleAt(img, fx, topFy)
			br, bg, bb, _ := sampleAt(img, fx, botFy)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
		}
		b.WriteString("\x1b[0m")
		if row < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

var (
	kittyOnce sync.Once
	kittyOK   bool
)

// resetKittyDetection clears the cached detection (test-only seam).
func resetKittyDetection() { kittyOnce = sync.Once{}; kittyOK = false }

// kittySupported reports whether the terminal supports the kitty graphics
// protocol, via environment heuristics, cached after the first call.
func kittySupported() bool {
	kittyOnce.Do(func() {
		if os.Getenv("KITTY_WINDOW_ID") != "" {
			kittyOK = true
			return
		}
		term := os.Getenv("TERM")
		prog := os.Getenv("TERM_PROGRAM")
		for _, hay := range []string{term, prog} {
			h := strings.ToLower(hay)
			if strings.Contains(h, "kitty") || strings.Contains(h, "wezterm") || strings.Contains(h, "ghostty") {
				kittyOK = true
				return
			}
		}
	})
	return kittyOK
}

// kittyChunk is the max base64 payload bytes per kitty escape. The protocol
// caps escape-code data at 4096 bytes; anything larger MUST be split across
// multiple escapes with m=1 (more follows) / m=0 (last) continuation framing.
// Terminals (e.g. ghostty) silently drop a single oversized escape, so a real
// PNG — always larger than 4096 base64 bytes — renders nothing without this.
const kittyChunk = 4096

// placeholderRune is the kitty Unicode placeholder code point (U+10EEEE). A
// terminal cell containing this rune, with its foreground color set to an image
// ID, is painted with the corresponding region of that image.
const placeholderRune = 0x10EEEE

// kitty renders img using the kitty graphics protocol's Unicode placeholder
// (virtual placement) mechanism: transmit the PNG once and create a virtual
// placement (U=1) spanning cols x rows cells, then emit a grid of placeholder
// cells whose foreground encodes the image ID. Unlike classic a=T cursor
// placement, placeholder cells are ordinary text cells — so they survive
// lipgloss layout and bubbletea's cell-diffing renderer, and they work through
// multiplexers. ghostty and kitty both implement this; other terminals fall
// back to half-blocks (callers gate on kittySupported via RenderTerminal).
//
// The base64 PNG is chunked at kittyChunk bytes: every escape carries m=1 until
// the final one carries m=0. q=2 suppresses the terminal's response codes so
// they don't corrupt the TUI stream.
func kitty(img image.Image, cols, rows, id int) string {
	if id <= 0 {
		id = 1
	}
	cols = clamp(cols, 1, len(rowColumnDiacritics))
	rows = clamp(rows, 1, len(rowColumnDiacritics))

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())

	var b strings.Builder
	first := true
	for len(payload) > 0 {
		n := min(kittyChunk, len(payload))
		chunk := payload[:n]
		payload = payload[n:]
		more := 1
		if len(payload) == 0 {
			more = 0
		}
		if first {
			fmt.Fprintf(&b, "\x1b_Ga=T,U=1,i=%d,q=2,f=100,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, chunk)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}

	// Placeholder grid. Each row's first cell carries the id-encoding foreground
	// color plus explicit row+column (col 0) diacritics; the remaining cells are
	// bare placeholders whose column auto-increments from the previous cell.
	ph := string(rune(placeholderRune))
	for r := 0; r < rows; r++ {
		fmt.Fprintf(&b, "\x1b[38;5;%dm%s%c%c", id, ph, rowColumnDiacritics[r], rowColumnDiacritics[0])
		for c := 1; c < cols; c++ {
			b.WriteString(ph)
		}
		b.WriteString("\x1b[0m")
		if r < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
