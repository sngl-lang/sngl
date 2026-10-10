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
import draw "sngl:ui/draw"

component art(rad float) node {
    draw.canvas(width=100px, height=100px) {
        draw.rect(x=0.0, y=0.0, w=10.0, h=5.0, style=draw.Style{fill=#ff0000, stroke=#0000ff, strokeWidth=2.0}) {}
        draw.circle(cx=5.0, cy=5.0, r=rad, style=draw.Style{fill=#00ff00}) {}
        draw.ellipse(cx=8.0, cy=8.0, rx=3.0, ry=2.0, style=draw.Style{stroke=#ff00ff, lineCap=round, lineJoin=bevel}) {}
        draw.line(x1=0.0, y1=0.0, x2=9.0, y2=9.0, style=draw.Style{stroke=#000000, strokeWidth=1.5}) {}
        draw.canvasText(x=1.0, y=9.0, content="hi", style=draw.Style{fill=#111111, fontSize=12.0}) {}
        draw.canvasImage(x=1.0, y=1.0, w=8.0, h=8.0, src="logo.png") {}
        draw.path(cmds=[draw.PathCmd{op=moveTo, x=0.0, y=0.0}, draw.PathCmd{op=lineTo, x=4.0, y=4.0}, draw.PathCmd{op=bezierTo, cx1=1.0, cy1=1.0, cx2=2.0, cy2=2.0, x=3.0, y=3.0}, draw.PathCmd{op=arcTo, cx1=5.0, cy1=0.0, x=5.0, y=5.0, r=2.0}, draw.PathCmd{op=close}], style=draw.Style{fill=#222222, stroke=#333333}) {}
    }
}

window(title="art") { art(rad=3.0) }
`
	buildGeneratedFiles(t, "gtk4-draw-", generateGTK4FilesBuilt(t, src))
}
