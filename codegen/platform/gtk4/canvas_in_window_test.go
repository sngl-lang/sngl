package gtk4

import "testing"

// passCanvas attached a canvas's draw func to whatever body held the canvas,
// and a `window` a component renders is an ir.Window statement in that
// component's body. CodegenCtx.AllFuncs walked only pkg.Windows, so a canvas
// under a component-declared window produced a BuildUI calling m._canvasDraw0
// against a method nothing declared: examples/calculator, whose seven-segment
// readout is exactly that shape.
//
// The radius is the *package's* so that the assertion can name it: a root
// component's own var is renamed per instantiation when the inliner splices
// the body in.
func TestACanvasUnderAComponentDeclaredWindowEmitsItsDrawFunc(t *testing.T) {
	files := generateGTK4FilesBuilt(t, `
import . "sngl:ui"
import . "sngl:ui/draw"

var radius = 50.0

component main root {
    window #w(title="c", href="/") {
        canvas(width=400px, height=280px) {
            circle(cx=200.0, cy=140.0, r=radius, style=CanvasStyle{fill=color{r=99, g=102, b=241, a=255}}) {}
        }
    }
}
`)
	buildGeneratedFiles(t, "gtk4-canvas-in-window-", files)
}
