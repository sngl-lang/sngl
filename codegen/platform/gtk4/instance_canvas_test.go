package gtk4

import (
	"strings"
	"testing"
)

// instanceCanvasSrc is a component that owns a `draw.canvas` and is met under a
// dynamic `for`, so the build cannot inline it and renders it as a record.
//
// The `var` is what makes it one: a component with no state of its own is
// still inlined into the loop body, and that path always worked.
const instanceCanvasSrc = `
import . "sngl:ui"
import . "sngl:ui/draw"

struct Item {
    level float = 0.0
}

component gauge(level = 0.0) {
    var hits = 0
    canvas(width=100px, height=100px) {
        rect(x=0.0, y=0.0, w=level, h=10.0) {}
    }
}

component main {
    var items list<Item> = [
        {level=10.0},
        {level=20.0},
    ]
    for var item = items {
        gauge(level=item.level)
    }
}
`

// TestInstanceCanvasEmitsDrawingArea asserts a canvas inside a runtime instance
// reaches the same GtkDrawingArea + cairo trampoline the Model path emits, and
// that the record it lands on is the one whose method the trampoline calls.
//
// Before the fix the instance translator carried no canvas metadata at all, so
// `canvas` fell through to the GIR widget lookup and the build failed with
// `component "canvas" has no gtk4 implementation`.
func TestInstanceCanvasEmitsDrawingArea(t *testing.T) {
	skipWithoutGIR(t)
	files := generateGTK4FilesBuilt(t, instanceCanvasSrc)
	model := files["model.go"]

	for _, want := range []string{
		// The drawing area is a field of the record, not of the Model.
		"type GaugeInstance struct {",
		"*C.GtkDrawingArea",
		"C.gtk_drawing_area_new()",
		// The trampoline closure calls the record's own draw method.
		"c._canvasDraw0(cr)",
		// which is typed as a cairo callback rather than a plain method.
		"func (c *GaugeInstance) _canvasDraw0(ctx *C.cairo_t) {",
		"C.cairo_rectangle(",
	} {
		if !strings.Contains(model, want) {
			t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
		}
	}
	// The Model has no receiver in the record's ctor, so a closure spelling
	// one does not compile.
	if strings.Contains(model, "m._canvasDraw0(cr)") {
		t.Errorf("the trampoline closure still calls the draw func on the Model\n--- model.go ---\n%s", model)
	}

	buildGeneratedFiles(t, "gtk4-inst-canvas-", files)
}
