//go:build !js

package html

import "testing"

// A canvas inside a component the build renders as a live instance draws, and
// keeps drawing when the instance's own state changes.
//
// Blank is the failure this catches, and it is silent: the page carried the
// <canvas> element and the draw function, and nothing ever called one with the
// other. Only a real browser can say the pixels arrived, which is why this is
// a browser test and not an assertion about the emitted JS.
func TestInstanceCanvasDrawsAndRedraws(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:app"
import . "sngl:ui/draw"

component gauge(level = 0.0) ui {
    var hits = 0.0
    button(text="hit", @click { hits += 20.0 })
    canvas(width=100px, height=100px) {
        rect(x=0.0, y=0.0, w=level + hits, h=40.0, style={fill=#ff0000}) {}
    }
}

component App() ui {
    var levels list<float> = [10.0, 20.0]
    for var l = levels {
        gauge(level=l)
    }
}

component main ui { window(title="C", href="/index.html") { App() } }
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	// The count of opaque red pixels in the first canvas: the rect's own area,
	// so it says both that the canvas drew and how wide the bar is.
	redPixels := func(i int) int {
		return page.MustEval(`(i) => {
			const el = document.querySelectorAll("canvas")[i];
			if (!el) return -1;
			const d = el.getContext("2d").getImageData(0, 0, el.width, el.height).data;
			let n = 0;
			for (let p = 0; p < d.length; p += 4) {
				if (d[p] > 200 && d[p+1] < 50 && d[p+2] < 50 && d[p+3] > 200) n++;
			}
			return n;
		}`, i).Int()
	}

	first := redPixels(0)
	if first <= 0 {
		t.Fatalf("the first instance's canvas is blank: %d red pixels", first)
	}
	// Each instance drew its own prop, so the wider bar belongs to the second.
	second := redPixels(1)
	if second <= first {
		t.Errorf("each instance draws its own level: got %d and %d red pixels", first, second)
	}

	page.MustElements("button")[0].MustClick()
	page.MustWaitStable()

	if got := redPixels(0); got <= first {
		t.Errorf("the click should redraw the first canvas wider: %d then %d red pixels", first, got)
	}
	if got := redPixels(1); got != second {
		t.Errorf("its sibling shares no state with it: %d then %d red pixels", second, got)
	}
}
