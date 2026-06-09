//go:build !js

package html

import (
	"strings"
	"testing"
)

// TestCanvasIntegration verifies the end-to-end HTML canvas pipeline:
// the generated output must contain a <canvas> element, a fillRect draw call,
// and the _snglColor color helper function.
func TestCanvasIntegration(t *testing.T) {
	src := `
output { none { html() } }
component main {
    window(title="Canvas Test", href="/index.html") {
        canvas(width=400px, height=300px) {
            rect(x=10.0, y=10.0, w=100.0, h=50.0) {}
        }
    }
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, "<canvas") {
		t.Errorf("expected <canvas element in HTML output\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "fillRect") && !strings.Contains(out, "_canvasDraw0") {
		t.Errorf("expected fillRect or _canvasDraw0 draw call in JS output\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "_snglColor") {
		t.Errorf("expected _snglColor helper in JS output\n--- generated ---\n%s", out)
	}
}
