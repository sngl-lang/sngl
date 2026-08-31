package fyne

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// TestCanvas_EmitsGGDrawAndRedraw verifies the fyne platform renders a
// `canvas` subtree to a gg-based software-raster draw function, creates a
// *canvas.Image widget sized to the canvas dimensions, and re-rasterises +
// Refreshes on a state-mutating handler.
func TestCanvas_EmitsGGDrawAndRedraw(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:ui/draw"
output { none { html } }
component main {
    var radius = 50.0
    canvas(width=400px, height=280px) {
        rect(x=0.0, y=0.0, w=400.0, h=280.0, style=CanvasStyle{fill=color{r=10, g=20, b=30, a=255}}) {}
        circle(cx=200.0, cy=140.0, r=radius, style=CanvasStyle{fill=color{r=99, g=102, b=241, a=255}, stroke=color{r=67, g=56, b=202, a=255}, strokeWidth=3.0}) {}
    }
    button(text="Grow", @click { radius = radius + 10.0 })
}
`
	pkg := checkForFyne(t, src)

	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	caps := g.Capabilities(lang).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	files := mem.Files()
	modelSrc, ok := files["model.go"]
	if !ok {
		t.Fatal("model.go not found in generated files")
	}
	out := string(modelSrc)

	for _, snippet := range []string{
		// draw func signature uses the shared *snglcanvas.Context runtime.
		"func (m *Model) _canvasDraw0(ctx *snglcanvas.Context)",
		// the SNGL canvas runtime is imported under the snglcanvas alias.
		`snglcanvas "git.duckfam.us/jonathan/sngl/pkg/go/canvas"`,
		// primitive draws go through Context methods.
		"ctx.Rect(",
		"ctx.Circle(",
		// style application sets fill/stroke via setters.
		"ctx.SetFill(",
		"ctx.SetStroke(",
		// canvas widget field + creation via the runtime.
		"snglcanvas.New(400, 280)",
		".Result()",
		"canvas.NewImageFromImage(",
		// ImageFillOriginal only grows the image once its renderer has run,
		// and the container lays out before that -- without an explicit min
		// size the canvas is one pixel tall.
		"SetMinSize(fyne.NewSize(400, 280))",
		// redraw wiring in the handler.
		"_canvasDraw0(",
		".Refresh()",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted Go missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}

	// Negative: the bespoke gg path is gone; no gg context or color helper.
	for _, leak := range []string{
		"gg.NewContext",
		"_snglColor",
		// untranslated canvas intrinsics must not leak.
		"CanvasDrawRect",
		"CanvasDrawCircle",
		"CanvasApplyStyle",
		"CanvasSave",
		"lower.CreateNode",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("untranslated intrinsic %q leaked into emitted Go", leak)
		}
	}

	// Compile-check under the main module so fyne + gg imports resolve.
	tmp, err := os.MkdirTemp(".", "fyne-canvas-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = tmp
	combined, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Errorf("emitted Go failed to compile: %v\n--- output ---\n%s\n--- source ---\n%s", buildErr, combined, out)
	}
}

// TestCanvas_RendersRealPixels builds and runs the generated fyne app via the
// Snapshot harness (real fyne test app + gg raster) and asserts the canvas
// produced non-uniform pixels — i.e. shapes actually drew. Not a mock: this
// exercises the whole pipeline end to end. Skipped in -short mode (compiles a
// fresh module, ~tens of seconds).
func TestCanvas_RendersRealPixels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-render snapshot in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	src := `
import . "sngl:ui"
import . "sngl:ui/draw"
output { none { html } }
component main {
    var radius = 60.0
    canvas(width=200px, height=200px) {
        rect(x=0.0, y=0.0, w=200.0, h=200.0, style=CanvasStyle{fill=color{r=240, g=240, b=240, a=255}}) {}
        circle(cx=100.0, cy=100.0, r=radius, style=CanvasStyle{fill=color{r=200, g=40, b=40, a=255}}) {}
    }
}
`
	pkg := checkForFyne(t, src)
	g := &Generator{}
	lang := codegen.LookupLang("go")
	caps := g.Capabilities(lang).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "fyne"}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	pngBytes, err := g.Snapshot(pkg, lang, 200, 200)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if !hasDistinctColors(img) {
		t.Errorf("canvas snapshot is uniform — no shapes appear to have rendered")
	}
}

// hasDistinctColors reports whether the image contains more than one distinct
// pixel color (a drawn shape over a background produces at least two).
func hasDistinctColors(img image.Image) bool {
	b := img.Bounds()
	var first uint32
	have := false
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, a := img.At(x, y).RGBA()
			key := r<<24 ^ g<<16 ^ bl<<8 ^ a
			if !have {
				first, have = key, true
				continue
			}
			if key != first {
				return true
			}
		}
	}
	return false
}
