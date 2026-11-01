package android

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// An SVG launcher icon fits the circle 66dp across every launcher mask
// leaves, corners included: scaled to the 72dp square instead, a square
// icon's corners were cut off by a round mask.
func TestAnIconFitsTheSafeZone(t *testing.T) {
	for _, size := range []struct{ w, h float64 }{{64, 64}, {100, 40}, {24, 48}} {
		svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g"><rect x="0" y="0" width="%g" height="%g"/></svg>`,
			size.w, size.h, size.w, size.h)
		vd, err := svgToVectorDrawable([]byte(svg))
		if err != nil {
			t.Fatal(err)
		}
		var tx, ty, sx, sy float64
		i := strings.Index(string(vd), "<group ")
		if i < 0 {
			t.Fatalf("no group in\n%s", vd)
		}
		if _, err := fmt.Sscanf(string(vd[i:]), `<group android:translateX="%g" android:translateY="%g" android:scaleX="%g" android:scaleY="%g">`, &tx, &ty, &sx, &sy); err != nil {
			t.Fatalf("reading the group: %v\n%s", err, vd)
		}
		for _, c := range [][2]float64{{0, 0}, {size.w, 0}, {0, size.h}, {size.w, size.h}} {
			x, y := tx+c[0]*sx, ty+c[1]*sy
			if r := math.Hypot(x-54, y-54); r > 33+1e-9 {
				t.Errorf("%gx%g: corner (%g,%g) lands %gdp from the center, outside the 33dp safe radius", size.w, size.h, c[0], c[1], r)
			}
		}
	}
}
