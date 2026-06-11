package canvas

import "testing"

func TestNewResultSize(t *testing.T) {
	c := New(40, 20)
	img := c.Result()
	b := img.Bounds()
	if b.Dx() != 40 || b.Dy() != 20 {
		t.Fatalf("Result size = %dx%d, want 40x20", b.Dx(), b.Dy())
	}
}
