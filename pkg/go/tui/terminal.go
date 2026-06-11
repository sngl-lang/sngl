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
func RenderTerminal(img image.Image, cols, rows int) string {
	if kittySupported() {
		return kitty(img)
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

// kitty encodes img as kitty graphics escape sequences (a=T: transmit +
// display). The base64 PNG is split into kittyChunk-sized segments: the first
// escape carries the format/action params, every escape carries m=1 until the
// last carries m=0. A payload that fits in one chunk is emitted as a single
// escape with no m key.
func kitty(img image.Image) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())

	// Single-chunk fast path: no continuation framing needed.
	if len(payload) <= kittyChunk {
		return "\x1b_Gf=100,a=T;" + payload + "\x1b\\"
	}

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
			fmt.Fprintf(&b, "\x1b_Gf=100,a=T,m=%d;%s\x1b\\", more, chunk)
			first = false
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return b.String()
}
