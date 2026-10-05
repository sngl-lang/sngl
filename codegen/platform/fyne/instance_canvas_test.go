package fyne

import (
	"fmt"
	"strings"
	"testing"
)

// instanceCanvasSrc is a component that owns a `draw.canvas` and is met under a
// dynamic `for`, so the build cannot inline it and renders it as a record.
//
// The `var` is what makes it one: a component with no state of its own is
// still inlined into the loop body, and that path always worked.
func instanceCanvasSrc(scaling string) string {
	mode := ""
	if scaling != "" {
		mode = ", scalingMode=ScalingMode." + scaling
	}
	return fmt.Sprintf(`
import . "sngl:ui"
import . "sngl:ui/draw"

struct Item {
    level float = 0.0
}

component gauge(level = 0.0) node {
    var hits = 0
    canvas(width=100px, height=100px%s) {
        rect(x=0.0, y=0.0, w=level, h=10.0) {}
    }
}

window {
    var items list<Item> = [
        {level=10.0},
        {level=20.0},
    ]
    for var item = items {
        gauge(level=item.level)
    }
}
`, mode)
}

// TestInstanceCanvasBelongsToTheRecord asserts a canvas inside a runtime
// instance rasterises through the record that holds it, in both of the two
// widgets a canvas becomes: an unscaled one is a canvas.Image drawn once, a
// scaled one a canvas.Raster whose generator draws at the size Fyne asks for.
//
// Before the fix the draw func came out `_canvasDraw0(ctx any)` on the record
// while the image field was referenced as a bare `__n1` the ctor never
// declared, and the Raster generator called `m.` -- a receiver that is not in
// scope inside a record's constructor.
func TestInstanceCanvasBelongsToTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scaling string
		want    []string
	}{
		{
			name: "unscaled",
			want: []string{
				"c.__n2Ctx = snglcanvas.New(100, 100)",
				"c._canvasDraw0(c.__n2Ctx)",
				"c.__n2 = canvas.NewImageFromImage(c.__n2Ctx.Result())",
			},
		},
		{
			name:    "scaled",
			scaling: "fit",
			want: []string{
				"c.__n2 = canvas.NewRaster(",
				`ctx := c.__n2Surface.Begin(pw, ph, 100, 100, "fit")`,
				"c._canvasDraw0(ctx)",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := generateFyneModelBuilt(t, instanceCanvasSrc(tc.scaling))
			want := append([]string{
				"type GaugeInstance struct {",
				// The draw func is typed as what it draws into.
				"func (c *GaugeInstance) _canvasDraw0(ctx *snglcanvas.Context) {",
			}, tc.want...)
			for _, w := range want {
				if !strings.Contains(model, w) {
					t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", w, model)
				}
			}
			// The Model has no receiver inside the record's ctor.
			for _, leak := range []string{"m._canvasDraw0(", "m.__n2"} {
				if strings.Contains(model, leak) {
					t.Errorf("the canvas still reaches the Model as %q\n--- model.go ---\n%s", leak, model)
				}
			}
			buildGeneratedGo(t, "fyne-inst-canvas-", model)
		})
	}
}
