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
