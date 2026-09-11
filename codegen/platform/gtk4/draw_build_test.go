//go:build !js

package gtk4

import "testing"

// Every shape this platform overrides, drawn once, and the emitted cgo
// compiled.
//
// Only a compiler can say the cairo is right: a native declaration names a C
// function and the argument conversions come from its declared parameter
// types, so a parameter typed `int` where cairo wants `cairo_line_cap_t`
// emits `C.int(...)` and does not build. That is exactly what this caught,
// twice -- the enum types, and then their names needing the `C.` prefix the
// call path adds but the type path does not.
func TestDrawOverridesCompile(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:ui/draw"

component art(rad float) node {
    canvas(width=100px, height=100px) {
        rect(x=0.0, y=0.0, w=10.0, h=5.0, style=CanvasStyle{fill=#ff0000, stroke=#0000ff, strokeWidth=2.0}) {}
        circle(cx=5.0, cy=5.0, r=rad, style=CanvasStyle{fill=#00ff00}) {}
        ellipse(cx=8.0, cy=8.0, rx=3.0, ry=2.0, style=CanvasStyle{stroke=#ff00ff, lineCap="round", lineJoin="bevel"}) {}
        line(x1=0.0, y1=0.0, x2=9.0, y2=9.0, style=CanvasStyle{stroke=#000000, strokeWidth=1.5}) {}
        canvasText(x=1.0, y=9.0, content="hi", style=CanvasStyle{fill=#111111, fontSize=12.0}) {}
        path(cmds=[PathCmd{op="moveTo", x=0.0, y=0.0}, PathCmd{op="lineTo", x=4.0, y=4.0}, PathCmd{op="bezierTo", cx1=1.0, cy1=1.0, cx2=2.0, cy2=2.0, x=3.0, y=3.0}, PathCmd{op="close"}], style=CanvasStyle{fill=#222222, stroke=#333333}) {}
    }
}

window(title="art", href="/index.html") { art(rad=3.0) }
`
	buildGeneratedFiles(t, "gtk4-draw-", generateGTK4FilesBuilt(t, src))
}
