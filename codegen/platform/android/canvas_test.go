package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// compileCanvasSrc parses, checks, lowers (with android caps) and emits the
// Kotlin for a canvas-bearing program, returning the generated source.
func compileCanvasSrc(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: androidTarget()})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	gen := &Generator{}
	kotlinLang := codegen.LookupLang("kotlin")
	if err := lower.Lower(pkg, gen.Capabilities(kotlinLang).ToLowerCaps(), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: pkg}, "android")
	out, err := CompileIR(ctx, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return string(out)
}

const canvasReactiveSrc = `import . "sngl://std"
import . "sngl://draw"
output {
    none { html }
}

component main {
    var radius = 50.0

    func circleStyle() => CanvasStyle{
        fill        = color{r=99, g=102, b=241, a=255},
        stroke      = color{r=67, g=56,  b=202, a=255},
        strokeWidth = 3.0,
    }

    vbox {
        canvas(width=400px, height=280px) {
            rect(x=0.0, y=0.0, w=400.0, h=280.0, style=circleStyle) {}
            circle(cx=200.0, cy=140.0, r=radius, style=circleStyle) {}
        }
        button(text="Grow", @click { radius = radius + 10.0 })
    }
}
`

func TestCanvasComposeEmission(t *testing.T) {
	code := compileCanvasSrc(t, canvasReactiveSrc)

	mustContain := func(want string) {
		t.Helper()
		if !strings.Contains(code, want) {
			t.Errorf("generated Kotlin missing %q\n---\n%s", want, code)
		}
	}

	// Compose Canvas composable, sized to the canvas pixel dimensions.
	mustContain("Canvas(")
	mustContain("Modifier.size(400.dp, 280.dp)")
	// DrawScope primitives for rect + circle.
	mustContain("drawRect(")
	mustContain("drawCircle(")
	// Color helper + stdlib data classes for the canvas structs.
	mustContain("fun _snglComposeColor(")
	mustContain("data class CanvasStyle(")
	mustContain("data class Color(")
	// The circle radius must read the reactive state var inside the draw
	// lambda so Compose recomposition redraws the canvas (no explicit
	// redraw call needed). In non-test mode the var is read bare.
	if !strings.Contains(code, "radius") {
		t.Errorf("draw should read reactive var 'radius'\n---\n%s", code)
	}

	// Style funcs must resolve, not emit the unresolved-method marker.
	if strings.Contains(code, "unresolved method") {
		t.Errorf("style call left unresolved\n---\n%s", code)
	}
	// The active style binds to a local and the fill draw is alpha-gated.
	mustContain("val _style1 = circleStyle")
	mustContain("_snglComposeColor(")

	// Balanced braces sanity.
	if o, c := strings.Count(code, "{"), strings.Count(code, "}"); o != c {
		t.Errorf("unbalanced braces: %d opens, %d closes", o, c)
	}
}

// TestCanvasTextRendersViaNativeCanvas guards that a canvas text shape draws
// real text (via the native canvas) rather than emitting a "not yet supported"
// no-op.
func TestCanvasTextRendersViaNativeCanvas(t *testing.T) {
	src := `import . "sngl://std"
import . "sngl://draw"
component main {
    canvas(width=200px, height=80px) {
        canvasText(x=10.0, y=40.0, content="hi", style=CanvasStyle{fill=color{r=10, g=20, b=30, a=255}, fontSize=14.0}) {}
    }
}`
	out := compileCanvasSrc(t, src)
	if strings.Contains(out, "not yet supported") {
		t.Errorf("canvas text still a no-op:\n%s", out)
	}
	for _, want := range []string{"nativeCanvas.drawText(", "android.graphics.Paint()", "\"hi\""} {
		if !strings.Contains(out, want) {
			t.Errorf("canvas text missing %q:\n%s", want, out)
		}
	}
}

// TestCanvasImageRenders guards that a canvasImage shape decodes + draws a
// bitmap (best-effort, local file path) rather than being a silent no-op.
func TestCanvasImageRenders(t *testing.T) {
	src := `import . "sngl://std"
import . "sngl://draw"
component main {
    canvas(width=100px, height=100px) {
        canvasImage(x=5.0, y=5.0, w=40.0, h=40.0, src="/tmp/p.png") {}
    }
}`
	out := compileCanvasSrc(t, src)
	for _, want := range []string{"BitmapFactory.decodeFile(", "asImageBitmap()", "drawImage(image ="} {
		if !strings.Contains(out, want) {
			t.Errorf("canvasImage missing %q:\n%s", want, out)
		}
	}
}
