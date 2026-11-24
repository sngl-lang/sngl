package android

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// An SVG launcher icon's longer side spans the 66dp circle every launcher
// mask keeps, centered on the 108dp canvas: at 72dp a round mask clipped its
// corners, and with its diagonal in the circle it came out far too small.
func TestAnIconFillsTheSafeZone(t *testing.T) {
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
		w, h := size.w*sx, size.h*sy
		if got := max(w, h); math.Abs(got-66) > 1e-9 {
			t.Errorf("%gx%g: longer side is %gdp, want 66", size.w, size.h, got)
		}
		if cx, cy := tx+w/2, ty+h/2; math.Abs(cx-54) > 1e-9 || math.Abs(cy-54) > 1e-9 {
			t.Errorf("%gx%g: centered at (%g,%g), want (54,54)", size.w, size.h, cx, cy)
		}
	}
}
