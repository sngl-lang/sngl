package snglimage

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"testing/fstest"
)

// A picture is decoded once: every later Load of it answers the image the
// first decoded, which a view drawn on every message would otherwise decode
// each time.
func TestLoadDecodesOnce(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 2))); err != nil {
		t.Fatal(err)
	}
	Embed(fstest.MapFS{"art.png": {Data: buf.Bytes()}})
	defer Embed(nil)
	first, err := Load("art.png")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load("/art.png")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Load("art.png")
	if first != again {
		t.Error("a second Load decoded the picture again")
	}
	if second.Bounds().Dx() != 4 {
		t.Errorf("decoded %v", second.Bounds())
	}
}
