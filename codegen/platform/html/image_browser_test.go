//go:build !js

package html

import (
	"strings"
	"testing"
)

// An image shows the source it holds: a drawing is painted into a picture the
// `<img>` shows and repainted when what it reads changes, a URL is the
// `<img>`'s `src`, and with no source the placeholder stands in its place.
// The window's drawn favicon is the page's icon.
func TestImageSources(t *testing.T) {
	src := `
import ui "sngl:ui"
import draw "sngl:ui/draw"

component dot(r float) ui.node {
    ui.image(alt="dot") {
        draw.image(width=20.0, height=20.0) {
            draw.rect(x=0.0, y=0.0, w=r, h=20.0, style={fill=#ff0000})
        }
    }
}

component App() ui.node {
    var w = 5.0
    var url = "https://example.com/a.png"
    var sizes list[float] = [4.0]
    ui.vbox {
        dot(r=w)
        ui.image(alt="remote") {
            ui.remote(url=url)
        }
        ui.avatar(initials="JD")
        for var s = sizes {
            dot(r=s + w)
        }
        ui.button(text="grow", @click {
            w = w + 5.0
            url = "https://example.com/b.png"
            sizes = [6.0]
        })
    }
}

ui.window(title="I") {
    App()
    component favicon {
        draw.image(width=16.0, height=16.0) {
            draw.rect(x=0.0, y=0.0, w=16.0, h=16.0, style={fill=#00ff00})
        }
    }
}
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	// The red pixels in the picture the i-th image named alt shows, once it
	// has decoded.
	redPixels := func(alt string, i int) int {
		return page.MustEval(`async (alt, i) => {
			const img = document.querySelectorAll('[aria-label="' + alt + '"] img')[i];
			if (!img) return -1;
			if (!img.complete || img.naturalWidth === 0) await img.decode().catch(() => {});
			if (img.naturalWidth === 0) return -2;
			const c = document.createElement("canvas");
			c.width = img.naturalWidth; c.height = img.naturalHeight;
			const ctx = c.getContext("2d");
			ctx.drawImage(img, 0, 0);
			const d = ctx.getImageData(0, 0, c.width, c.height).data;
			let n = 0;
			for (let p = 0; p < d.length; p += 4) {
				if (d[p] > 200 && d[p+1] < 50 && d[p+2] < 50 && d[p+3] > 200) n++;
			}
			return n;
		}`, alt, i).Int()
	}

	first := redPixels("dot", 0)
	if first != 5*20 {
		t.Fatalf("the drawing shows a 5x20 bar: %d red pixels", first)
	}
	if got := redPixels("dot", 1); got != 9*20 {
		t.Errorf("the drawing built at run time shows a 9x20 bar: %d red pixels", got)
	}
	if got := page.MustEval(`() => document.querySelector('[aria-label="remote"] img').getAttribute("src")`).String(); got != "https://example.com/a.png" {
		t.Errorf("the remote image's source names its url: %q", got)
	}

	// The avatar with no source shows its initials in the image's place.
	if got := page.MustEval(`() => document.querySelectorAll("img").length`).Int(); got != 3 {
		t.Errorf("the avatar with no source renders no <img>: %d images", got)
	}
	if got := page.MustEval(`() => [...document.querySelectorAll("span")].some(s => s.textContent === "JD")`).Bool(); !got {
		t.Errorf("the placeholder stands in the image's place")
	}

	if got := page.MustEval(`() => document.querySelector('link[rel="icon"]').href`).String(); !strings.HasPrefix(got, "data:image/png") {
		t.Errorf("the drawn favicon is the page's icon: %q", got)
	}

	page.MustElement("button").MustClick()
	page.MustWaitStable()

	if got := redPixels("dot", 0); got != 10*20 {
		t.Errorf("the click repaints the drawing 10 wide: %d red pixels", got)
	}
	if got := redPixels("dot", 1); got != 16*20 {
		t.Errorf("the instance built at run time repaints 16 wide: %d red pixels", got)
	}
	if got := page.MustEval(`() => document.querySelector('[aria-label="remote"] img').getAttribute("src")`).String(); got != "https://example.com/b.png" {
		t.Errorf("the remote image follows its url: %q", got)
	}
}
