package gtk4

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestCanvas_EmitsCairoDrawAndRedraw verifies the gtk4 platform renders a
// `canvas` subtree to a cairo-based draw function attached to a
// GtkDrawingArea, and that a state-mutating handler queues a redraw.
func TestCanvas_EmitsCairoDrawAndRedraw(t *testing.T) {
	src := `
component main {
    var radius = 50.0
    canvas(width=400px, height=280px) {
        rect(x=0.0, y=0.0, w=400.0, h=280.0, style=CanvasStyle{fill=color{r=10, g=20, b=30, a=255}}) {}
        circle(cx=200.0, cy=140.0, r=radius, style=CanvasStyle{fill=color{r=99, g=102, b=241, a=255}, stroke=color{r=67, g=56, b=202, a=255}, strokeWidth=3.0}) {}
    }
    button(text="Grow", @click { radius = radius + 10.0 })
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s", d.Msg)
		}
	}

	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	caps := g.Capabilities(lang).ToLowerCaps()
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "gtk4"}); err != nil {
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
		// draw func signature takes a *C.cairo_t.
		"func (m *Model) _canvasDraw0(ctx *C.cairo_t)",
		// drawing-area construction + sizing.
		"C.gtk_drawing_area_new",
		"C.gtk_drawing_area_set_content_width",
		"C.gtk_drawing_area_set_content_height",
		// draw-func trampoline registration.
		"sngl_drawing_area_set_draw",
		// cairo primitives.
		"C.cairo_rectangle",
		"C.cairo_arc",
		"C.cairo_set_source_rgba",
		"C.cairo_fill",
		// redraw wiring in the handler.
		"C.gtk_widget_queue_draw",
	} {
		if !strings.Contains(out, snippet) {
			t.Errorf("emitted model.go missing snippet %q\n--- generated ---\n%s", snippet, out)
		}
	}

	// The draw trampoline glue + dispatch live in callbacks.go.
	cbSrc, ok := files["callbacks.go"]
	if !ok {
		t.Fatal("callbacks.go not found in generated files")
	}
	cb := string(cbSrc)
	for _, snippet := range []string{
		"snglDrawFuncs",
		"//export sngl_draw_dispatch",
	} {
		if !strings.Contains(cb, snippet) {
			t.Errorf("emitted callbacks.go missing snippet %q\n--- generated ---\n%s", snippet, cb)
		}
	}

	// Negative: untranslated canvas intrinsics must not leak.
	for _, leak := range []string{
		"CanvasDrawRect",
		"CanvasDrawCircle",
		"CanvasApplyStyle",
		"CanvasSave",
		"CanvasRedrawStmt",
		"lower.CreateNode",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("untranslated intrinsic %q leaked into emitted Go", leak)
		}
	}
}
