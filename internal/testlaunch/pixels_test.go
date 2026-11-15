package testlaunch

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// A C agent's snapshot is its pixels, which the driver keeps as the PNG any
// other agent's is.
func TestEncodePixelsIsThePNGOfThem(t *testing.T) {
	pix := []byte{
		255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255,
		10, 20, 30, 255, 40, 50, 60, 128, 70, 80, 90, 0,
	}
	data, err := encodePixels(bytes.Clone(pix), 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds(); got != image.Rect(0, 0, 3, 2) {
		t.Fatalf("bounds %v, want 3 by 2", got)
	}
	n, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("decoded a %T, want *image.NRGBA", img)
	}
	if !bytes.Equal(n.Pix, pix) {
		t.Fatalf("pixels %v, want %v", n.Pix, pix)
	}
}

func TestEncodePixelsRefusesAWrongCount(t *testing.T) {
	if _, err := encodePixels(make([]byte, 11), 3, 1); err == nil {
		t.Fatal("11 bytes taken for 3 by 1")
	}
	if _, err := encodePixels(nil, 0, 0); err == nil {
		t.Fatal("an empty picture taken")
	}
}
