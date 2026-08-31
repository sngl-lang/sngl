//go:build linux

package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"os"
	"regexp"
	"strings"
	"testing"
)

// solid returns an opaque w x h image whose pixels are all c.
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

// shmName extracts and decodes the object name from a t=s escape.
func shmName(t *testing.T, esc string) string {
	t.Helper()
	m := regexp.MustCompile(`;([^\x1b]+)\x1b\\\\?`).FindStringSubmatch(esc)
	if m == nil {
		t.Fatalf("no payload in %q", esc)
	}
	raw, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	return string(raw)
}

// localKitty makes both probes see a local terminal that takes shared memory,
// regardless of what the test machine is actually running under.
func localKitty(t *testing.T) {
	t.Helper()
	for _, v := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	resetShmDetection()
	resetKittyDetection()
	t.Cleanup(resetShmDetection)
	t.Cleanup(resetKittyDetection)
}

func TestShmTransmitCarriesRawPixels(t *testing.T) {
	localKitty(t)
	if !shmSupported() {
		t.Skip("no usable /dev/shm")
	}
	img := solid(3, 2, color.RGBA{1, 2, 3, 255})
	esc, ok := kittyTransmitSHM(img, 4, 1, 7)
	if !ok {
		t.Fatal("shared memory transport declined a plain opaque image")
	}
	// s/v are pixels and c/r are cells: the terminal needs both to place the
	// image, and only the pixel dimensions describe the buffer.
	for _, want := range []string{"t=s", "f=32", "s=3", "v=2", "c=4", "r=1", "i=7"} {
		if !strings.Contains(esc, want) {
			t.Errorf("missing %q in %q", want, esc)
		}
	}
	// The point of the transport: the frame is not in the escape.
	if len(esc) > 200 {
		t.Errorf("escape carries the image (%d bytes): %q", len(esc), esc)
	}
	got, err := os.ReadFile(shmPath(shmName(t, esc)))
	if err != nil {
		t.Fatalf("reading the handed-over object: %v", err)
	}
	if !bytes.Equal(got, img.Pix) {
		t.Errorf("object holds %d bytes, image is %d", len(got), len(img.Pix))
	}
}

func TestShmCarriesTranslucentPixelsStraight(t *testing.T) {
	localKitty(t)
	if !shmSupported() {
		t.Skip("no usable /dev/shm")
	}
	// Go premultiplies and f=32 does not, so half-alpha red stored as 0x80 has
	// to arrive as the 0xff it was drawn with, or the frame renders dark.
	img := solid(2, 2, color.RGBA{0x80, 0, 0, 0x80})
	esc, ok := kittyTransmitSHM(img, 1, 1, 1)
	if !ok {
		t.Fatal("declined a translucent frame")
	}
	got, err := os.ReadFile(shmPath(shmName(t, esc)))
	if err != nil {
		t.Fatalf("reading the handed-over object: %v", err)
	}
	for i := 0; i+3 < len(got); i += 4 {
		if want := [4]byte{0xff, 0, 0, 0x80}; [4]byte(got[i:i+4]) != want {
			t.Fatalf("pixel %d is %v, want %v", i/4, got[i:i+4], want)
		}
	}
}

func TestStraightRGBAKeepsInvisiblePixelsClear(t *testing.T) {
	// Premultiplied by zero leaves no colour to divide back out, and dividing
	// would be by zero.
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.Pix[4], img.Pix[5], img.Pix[6], img.Pix[7] = 0x40, 0x40, 0x40, 0xff
	dst := make([]byte, len(img.Pix))
	straightRGBA(dst, img)
	if !bytes.Equal(dst[:4], []byte{0, 0, 0, 0}) {
		t.Errorf("transparent pixel became %v", dst[:4])
	}
	if !bytes.Equal(dst[4:], img.Pix[4:]) {
		t.Errorf("opaque pixel became %v", dst[4:])
	}
}

func TestShmDeclinesOverSSH(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/pts/3")
	resetShmDetection()
	t.Cleanup(resetShmDetection)
	if shmSupported() {
		t.Error("offered a local memory object to a terminal on another machine")
	}
}

func TestShmDeclinedByTerminalWithoutTheMedium(t *testing.T) {
	localKitty(t)
	// A terminal that draws kitty images but does not implement the medium
	// answers t=s with an error, which loses the frame rather than retrying it
	// another way.
	t.Setenv("KITTY_WINDOW_ID", "")
	_ = os.Unsetenv("KITTY_WINDOW_ID")
	t.Setenv("TERM_PROGRAM", "WezTerm")
	resetKittyDetection()
	if !kittySupported() {
		t.Fatal("wezterm should still draw images")
	}
	if kittySHMSupported() {
		t.Error("offered shared memory to a terminal not known to read it")
	}
}

func TestKittyTransmitFallsBackToPNG(t *testing.T) {
	localKitty(t)
	// The terminal takes shared memory; the machine it runs on is not this one.
	t.Setenv("SSH_TTY", "/dev/pts/3")
	resetShmDetection()
	esc := kittyTransmit(solid(4, 4, color.RGBA{9, 9, 9, 255}), 2, 1, 1)
	if !strings.Contains(esc, "f=100") || strings.Contains(esc, "t=s") {
		t.Errorf("expected the pty path to compress, got %q", esc)
	}
}

func TestShmUnlinksOldObjects(t *testing.T) {
	localKitty(t)
	if !shmSupported() {
		t.Skip("no usable /dev/shm")
	}
	put := func() (string, bool) { return shmPut(4, func(m []byte) { copy(m, []byte{1, 2, 3, 4}) }) }
	first, ok := put()
	if !ok {
		t.Fatal("shmPut")
	}
	for range 2 {
		if _, ok := put(); !ok {
			t.Fatal("shmPut")
		}
	}
	if _, err := os.Stat(shmPath(first)); !os.IsNotExist(err) {
		_ = os.Remove(shmPath(first))
		t.Errorf("the first object outlived two transmits: %v", err)
	}
}
