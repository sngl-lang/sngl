//go:build !js

package html

import (
	"strings"
	"testing"
)

// TestCanvasReactiveRedraw verifies that a canvas referencing a state variable
// produces a reactive updater body containing clearRect and the draw function
// call, so the canvas redraws whenever the state changes.
func TestCanvasReactiveRedraw(t *testing.T) {
	src := `
import . "sngl:app"
import . "sngl:test"
import . "sngl:ui/draw"
output { none { html() } }
var size float = 100.0

window(title="Canvas Reactive Test", href="/index.html") {
    canvas(width=400px, height=300px) {
        rect(x=10.0, y=10.0, w=size, h=50.0) {}
    }
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, "clearRect") {
		t.Errorf("expected clearRect in canvas updater body\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "_canvasDraw0") {
		t.Errorf("expected _canvasDraw0 call in output\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "_snglColor") {
		t.Errorf("expected _snglColor helper in JS output\n--- generated ---\n%s", out)
	}
}

// TestCanvasIntegration verifies the end-to-end HTML canvas pipeline:
// the generated output must contain a <canvas> element, a fillRect draw call,
// and the _snglColor color helper function.
func TestCanvasIntegration(t *testing.T) {
	src := `
import . "sngl:app"
import . "sngl:test"
import . "sngl:ui/draw"
output { none { html() } }
window(title="Canvas Test", href="/index.html") {
    canvas(width=400px, height=300px) {
        rect(x=10.0, y=10.0, w=100.0, h=50.0) {}
    }
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, "<canvas") {
		t.Errorf("expected <canvas element in HTML output\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "fillRect") {
		t.Errorf("expected fillRect draw call in JS output\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "_snglColor") {
		t.Errorf("expected _snglColor helper in JS output\n--- generated ---\n%s", out)
	}
}
