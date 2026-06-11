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

func TestKittyTransmitHasVirtualPlacement(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	out := kittyTransmit(img, 4, 2, 1)
	// Transmit escape: virtual placement (U=1) carrying the image id + cell span.
	if !strings.HasPrefix(out, "\x1b_Ga=T,U=1,i=1,q=2,f=100,c=4,r=2,") {
		t.Errorf("transmit must start with a virtual-placement escape, got %.50q", out)
	}
	if !strings.HasSuffix(out, "\x1b\\") {
		t.Errorf("transmit must end with the APC terminator (ST)")
	}
	// The transmit is pure APC data — no placeholder cells in it.
	if strings.Contains(out, string(rune(placeholderRune))) {
		t.Errorf("transmit must not contain placeholder cells (those are RenderTerminal's job)")
	}
}

func TestKittyPlaceholderGridDimensions(t *testing.T) {
	out := kittyPlaceholders(5, 3, 7)
	// No image data — placeholders only.
	if strings.Contains(out, "\x1b_G") {
		t.Errorf("placeholder grid must contain no transmit (APC) escapes")
	}
	if !strings.Contains(out, "\x1b[38;5;7m") {
		t.Errorf("placeholder cells must encode the image id (7) in the foreground color")
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 placeholder rows, got %d: %q", len(lines), out)
	}
	for r, ln := range lines {
		if got := strings.Count(ln, string(rune(placeholderRune))); got != 5 {
			t.Errorf("row %d: want 5 placeholder cells, got %d", r, got)
		}
	}
}

func TestKittyTransmitChunksLargePayload(t *testing.T) {
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
	out := kittyTransmit(img, 10, 5, 1)

	escapes := strings.Count(out, "\x1b_G")
	if escapes < 2 {
		t.Fatalf("large image must chunk into multiple escapes, got %d", escapes)
	}
	if !strings.HasPrefix(out, "\x1b_Ga=T,U=1,i=1,q=2,f=100,c=10,r=5,m=1;") {
		t.Errorf("first chunk must carry the full transmit params + m=1, got prefix %.60q", out)
	}
	if strings.Count(out, "m=1;") != escapes-1 {
		t.Errorf("want %d continuation (m=1) chunks, got %d", escapes-1, strings.Count(out, "m=1;"))
	}
	if strings.Count(out, "m=0;") != 1 {
		t.Errorf("want exactly one terminating m=0 chunk, got %d", strings.Count(out, "m=0;"))
	}
	// Each transmit escape's base64 payload must respect the protocol chunk cap.
	for _, seg := range strings.Split(out, "\x1b_G")[1:] {
		esc, _, _ := strings.Cut(seg, "\x1b\\")
		_, data, _ := strings.Cut(esc, ";")
		if len(data) > kittyChunk {
			t.Errorf("chunk payload %d exceeds cap %d", len(data), kittyChunk)
		}
	}
}

func TestKittyTransmitSuppressedWithoutSupport(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	resetKittyDetection()
	called := false
	render := func() image.Image { called = true; return image.NewNRGBA(image.Rect(0, 0, 4, 4)) }
	if got := KittyTransmit(4, 2, 1, render); got != "" {
		t.Errorf("KittyTransmit on a non-kitty terminal must be empty, got %.30q", got)
	}
	if called {
		t.Errorf("KittyTransmit must not rasterise (call render) when kitty is unsupported")
	}
}

func TestKittyTransmitSkipsUnchanged(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	resetKittyDetection()
	resetTransmitCache()
	mk := func() image.Image {
		im := image.NewNRGBA(image.Rect(0, 0, 8, 8))
		im.Set(2, 2, color.NRGBA{1, 2, 3, 255})
		return im
	}
	first := KittyTransmit(8, 4, 99, mk)
	if first == "" {
		t.Fatal("first transmit must emit the image data")
	}
	if again := KittyTransmit(8, 4, 99, mk); again != "" {
		t.Errorf("identical pixels must skip re-transmit, got %d bytes", len(again))
	}
	// A different image for the same id must transmit again.
	changed := KittyTransmit(8, 4, 99, func() image.Image {
		im := image.NewNRGBA(image.Rect(0, 0, 8, 8))
		im.Set(5, 5, color.NRGBA{9, 9, 9, 255})
		return im
	})
	if changed == "" {
		t.Errorf("changed pixels must re-transmit")
	}
}

func TestRenderTerminalKittySkipsRasterise(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	resetKittyDetection()
	called := false
	out := RenderTerminal(5, 3, 1, func() image.Image { called = true; return nil })
	if called {
		t.Errorf("kitty path must not rasterise — placeholders need no pixels")
	}
	if !strings.Contains(out, string(rune(placeholderRune))) {
		t.Errorf("kitty RenderTerminal must return the placeholder grid")
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
