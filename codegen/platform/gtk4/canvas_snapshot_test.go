//go:build !js

package gtk4_test

import (
	"bytes"
	"image"
	"image/png"
	"os/exec"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestCanvas_RendersRealPixels builds and runs the generated gtk4 app through
// the cairo snapshot harness (GskCairoRenderer) and asserts the canvas
// produced non-uniform pixels — i.e. shapes actually drew. Requires GTK4 dev
// libs + a display; skips otherwise. Skipped in -short mode (compiles a fresh
// cgo module, tens of seconds).
func TestCanvas_RendersRealPixels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-render snapshot in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	// Presents a real GtkWindow — only safe inside a headless compositor.
	// Skips (rather than flashing) when cage is unavailable; see TestMain.
	if r := testutil.GUIRenderSkipReason(); r != "" {
		t.Skip(r)
	}
	// Legitimately skip only when the GTK4 dev libraries are absent (cgo
	// could not link). If pkg-config reports gtk4 present, the environment
	// is capable and any subsequent failure is a real compile/run bug —
	// which must fail the test, not be masked as an env skip.
	if pc, err := exec.LookPath("pkg-config"); err != nil {
		t.Skip("pkg-config not available; cannot confirm gtk4 dev libs")
	} else if err := exec.Command(pc, "--exists", "gtk4").Run(); err != nil {
		t.Skip("gtk4 dev libraries not installed")
	}

	src := `
import . "sngl:ui"
import . "sngl:ui/draw"
window {
    var radius = 60.0
    canvas(width=200px, height=200px) {
        rect(x=0.0, y=0.0, w=200.0, h=200.0, style=CanvasStyle{fill=color{r=240, g=240, b=240, a=255}}) {}
        circle(cx=100.0, cy=100.0, r=radius, style=CanvasStyle{fill=color{r=200, g=40, b=40, a=255}}) {}
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "gtk4", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}
	g := &gtk4.Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}

	// GTK4 dev libs + display are confirmed present above, so a Snapshot
	// failure here means the generated cgo code failed to compile or the
	// program crashed at runtime — a real bug. Fail, do not skip.
	pngBytes, err := g.Snapshot(pkg, lang, 200, 200)
	if err != nil {
		t.Fatalf("snapshot failed (generated code did not compile/run): %v", err)
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
			r, gg, bl, a := img.At(x, y).RGBA()
			key := r<<24 ^ gg<<16 ^ bl<<8 ^ a
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
