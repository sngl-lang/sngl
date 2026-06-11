package tui

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestHalfBlockKnownImage(t *testing.T) {
	// 2x2: top row red, bottom row blue. One cell wide, one cell tall.
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(1, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(0, 1, color.NRGBA{0, 0, 255, 255})
	img.Set(1, 1, color.NRGBA{0, 0, 255, 255})

	out := halfBlock(img, 2, 1)
	if !strings.Contains(out, "▀") {
		t.Errorf("expected upper-half-block ▀ in output, got %q", out)
	}
	if !strings.Contains(out, "38;2;255;0;0") { // fg = top = red
		t.Errorf("expected red foreground truecolor, got %q", out)
	}
	if !strings.Contains(out, "48;2;0;0;255") { // bg = bottom = blue
		t.Errorf("expected blue background truecolor, got %q", out)
	}
}

func TestKittyEncodeHasGraphicsEscape(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	out := kitty(img)
	if !strings.HasPrefix(out, "\x1b_G") {
		t.Errorf("kitty output must start with the graphics escape, got %q", out)
	}
	if !strings.HasSuffix(out, "\x1b\\") {
		t.Errorf("kitty output must end with ST, got %q", out)
	}
}

func TestKittyChunksLargePayload(t *testing.T) {
	// A 256x256 LCG-noise image PNG-encodes past one 4096-byte chunk (a smooth
	// gradient compresses too well to force chunking).
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	var s uint32 = 1
	next := func() uint8 { s = s*1664525 + 1013904223; return uint8(s >> 24) }
	for y := range 256 {
		for x := range 256 {
			img.Set(x, y, color.NRGBA{next(), next(), next(), 255})
		}
	}
	out := kitty(img)

	escapes := strings.Count(out, "\x1b_G")
	if escapes < 2 {
		t.Fatalf("large image must chunk into multiple escapes, got %d", escapes)
	}
	if !strings.HasPrefix(out, "\x1b_Gf=100,a=T,m=1;") {
		t.Errorf("first chunk must carry params + m=1, got prefix %.40q", out)
	}
	if strings.Count(out, "m=1;") != escapes-1 {
		t.Errorf("want %d continuation (m=1) chunks, got %d", escapes-1, strings.Count(out, "m=1;"))
	}
	if strings.Count(out, "m=0;") != 1 {
		t.Errorf("want exactly one terminating m=0 chunk, got %d", strings.Count(out, "m=0;"))
	}
	if !strings.HasSuffix(out, "\x1b\\") {
		t.Errorf("output must end with ST")
	}
	for _, seg := range strings.Split(out, "\x1b_G")[1:] {
		seg = strings.TrimSuffix(seg, "\x1b\\")
		_, data, _ := strings.Cut(seg, ";")
		if len(data) > kittyChunk {
			t.Errorf("chunk payload %d exceeds cap %d", len(data), kittyChunk)
		}
	}
}

func TestKittyDetectionFromEnv(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	resetKittyDetection()
	if !kittySupported() {
		t.Error("KITTY_WINDOW_ID set should report kitty supported")
	}
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	resetKittyDetection()
	if kittySupported() {
		t.Error("plain xterm should not report kitty supported")
	}
}
