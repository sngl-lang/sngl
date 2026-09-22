package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"

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
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: androidTarget(), Languages: androidLangs(), Targets: []ir.StaticTarget{{Platform: "android", Language: "kotlin"}}})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	gen := &Generator{}
	kotlinLang := codegen.LookupLang("kotlin")
	if err := lower.Lower(pkg, codegen.CapsOrNone(kotlinLang.LanguageIdentifier(), gen.PlatformIdentifier()), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	ctx := codegen.NewCodegenCtx(&codegen.Request{Doc: doc, Pkg: pkg}, "android")
	out, err := CompileIR(ctx, Config{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return string(out)
}

const canvasReactiveSrc = `import . "sngl:ui"
import . "sngl:ui/draw"
import ui "sngl:ui"
output {
    none { html }
}

ui.window {
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
	// The stdlib data classes for the canvas structs.
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
	// The style reaches the draw call, and the paint is alpha-gated. `circle`
	// draws from this platform's override now, which sets the colour inline
	// from the style it was given -- the `_style1` local and the
	// `_snglComposeColor` helper both belonged to the CanvasApplyStyle
	// expansion the override replaces.
	mustContain("circleStyle.fill")
	mustContain("circleStyle.fill.a > 0")

	// Balanced braces sanity.
	if o, c := strings.Count(code, "{"), strings.Count(code, "}"); o != c {
		t.Errorf("unbalanced braces: %d opens, %d closes", o, c)
	}
}

// TestCanvasTextRendersViaNativeCanvas guards that a canvas text shape draws
// real text (via the native canvas) rather than emitting a "not yet supported"
// no-op.
func TestCanvasTextRendersViaNativeCanvas(t *testing.T) {
	src := `import . "sngl:ui"
import . "sngl:ui/draw"
import ui "sngl:ui"
ui.window {
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
	src := `import . "sngl:ui"
import . "sngl:ui/draw"
import ui "sngl:ui"
ui.window {
    canvas(width=100px, height=100px) {
        canvasImage(x=5.0, y=5.0, w=40.0, h=40.0, src="/tmp/p.png") {}
    }
}`
	out := compileCanvasSrc(t, src)
	// The call site first: the shim is emitted for any canvas, so asserting
	// its body alone would pass with no image in the program at all.
	for _, want := range []string{
		`snglDrawImage(src = "/tmp/p.png"`,
		"BitmapFactory.decodeFile(", "asImageBitmap()", "drawImage(",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("canvasImage missing %q:\n%s", want, out)
		}
	}
}

// A struct binding is copied so a mutation cannot reach whoever else holds the
// value -- Compose decides whether to recompose by structural equality, and a
// handler that mutated one in place left the screen unchanged. A binding
// nothing writes has no mutation to contain, and `passMutatedVars` is what
// tells the two apart.
//
// Both directions, because the failure modes are opposite: a missing copy
// aliases two names silently, and a spurious one is only waste.
func TestOnlyAMutatedBindingIsCopied(t *testing.T) {
	code := compileSrc(t, `
import . "sngl:ui"
import time "sngl:time"

struct Item {
    label string = ""
    count int = 0
}

component main node {
    var src = Item{label="a", count=1}
    var out = 0

    // A timer handler as well as a click one. The pass that answers this used
    // to walk a list of roots written out by hand, which named component
    // bodies and their funcs and missed Timers entirely -- so a binding
    // written here was read as written nowhere, the copy was skipped, and the
    // in-place write left Compose's structural equality saying nothing had
    // changed. Exactly the bug the copy exists to prevent.
    time.timer(interval=1s, @tick {
        var ticked = src
        ticked.count = 9
        out = ticked.count
    })

    button(text="go", @click {
        var readOnly = src
        var written = src
        var viaLambda = src

        var bump = func() { viaLambda.count = 7 }
        bump()
        written.count = 3
        out = readOnly.count + written.count + viaLambda.count
    })
}

window(title="t", href="/index.html") { main() }
`, false)

	for _, want := range []string{
		"var written = src__inst0.copy()",
		"var viaLambda = src__inst0.copy()",
		"var ticked = src__inst0.copy()",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("a written binding must be copied, missing %q\n---\n%s", want, code)
		}
	}
	if !strings.Contains(code, "var readOnly = src__inst0\n") {
		t.Errorf("a binding nothing writes needs no copy\n---\n%s", code)
	}
}
