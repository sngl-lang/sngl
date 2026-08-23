package snglcolor

import "testing"

// Hex is what a program sees when it prints a colour, and it has to agree with
// the compiler's own folding of the same call. It dropped alpha until the
// channels became part of the value, so two different colours printed alike.
func TestHexHonoursAlpha(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Color
		want string
	}{
		{"opaque", Color{R: 255, G: 0, B: 0, A: 255}, "#ff0000"},
		{"translucent", Color{R: 255, G: 0, B: 0, A: 128}, "#ff000080"},
		{"transparent", Color{R: 1, G: 2, B: 3, A: 0}, "#01020300"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Hex(); got != tc.want {
				t.Errorf("Hex() = %q; want %q", got, tc.want)
			}
		})
	}
}

// A zero Color is fully transparent, so a struct literal that omits alpha must
// be completed with the declared default before it reaches here — the compiler
// does that now, and this pins what happens if it stops.
func TestZeroColorIsTransparent(t *testing.T) {
	if got := (Color{}).Hex(); got != "#00000000" {
		t.Errorf("zero Color Hex() = %q; want %q", got, "#00000000")
	}
}
