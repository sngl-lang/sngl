package gtk4

import "testing"

// passCanvas attaches a canvas's draw func to whatever body holds the canvas,
// and a `window` written inside a component is an ir.Window statement in that
// component's body -- not one of pkg.Windows. CodegenCtx.AllFuncs walked only
// pkg.Windows, so a canvas under a component-declared window produced a
// BuildUI calling m._canvasDraw0 against a method nothing declared:
// examples/calculator, whose seven-segment readout is exactly that shape.
func TestACanvasUnderAComponentDeclaredWindowEmitsItsDrawFunc(t *testing.T) {
	files := generateGTK4FilesBuilt(t, `
import . "sngl:ui"
import . "sngl:app"
import . "sngl:ui/draw"

component main ui {
    var radius = 50.0

    window #w(title="c", href="/") {
        canvas(width=400px, height=280px) {
            circle(cx=200.0, cy=140.0, r=radius, style=CanvasStyle{fill=color{r=99, g=102, b=241, a=255}}) {}
        }
    }
}
`)
	buildGeneratedFiles(t, "gtk4-canvas-in-window-", files)
}
