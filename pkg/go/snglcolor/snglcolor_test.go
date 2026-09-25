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

// A zero Color is fully transparent, which is why a struct literal that omits
// alpha has to arrive here already completed with its declared default.
func TestZeroColorIsTransparent(t *testing.T) {
	if got := (Color{}).Hex(); got != "#00000000" {
		t.Errorf("zero Color Hex() = %q; want %q", got, "#00000000")
	}
}

func TestOrFallsBackOnlyForTheUnsetColor(t *testing.T) {
	if got := (Color{}).Or("5"); got != "5" {
		t.Errorf("unset.Or = %q, want the fallback", got)
	}
	if got := (Color{R: 255, A: 255}).Or("5"); got != "#ff0000" {
		t.Errorf("set.Or = %q, want the color", got)
	}
}
