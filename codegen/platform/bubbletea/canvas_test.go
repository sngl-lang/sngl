package bubbletea

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestBubbleteaCanvasEmission(t *testing.T) {
	src := `import . "sngl:ui"
import . "sngl:ui/draw"
component main {
    canvas(width=40px, height=20px) {
        rect(x=0.0, y=0.0, w=40.0, h=20.0, style=CanvasStyle{fill=color{r=10, g=20, b=30, a=255}}) {}
    }
}`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Targets: []ir.StaticTarget{{Platform: "bubbletea", Language: "go"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "bubbletea"}); err != nil {
		t.Fatal(err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	var out strings.Builder
	for _, f := range mem.Files() {
		out.WriteString(string(f))
	}
	for _, want := range []string{
		"var _canvasSurface0 snglcanvas.Surface",
		"_canvasSurface0.Begin(40, 20, 0, 0, \"\")",
		"ctx.Rect(",
		"RenderTerminal(",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in emitted bubbletea output:\n%s", want, out.String())
		}
	}
	// The surface is what keeps the buffer across frames; allocating one per
	// rasterise is the thing it replaced.
	for _, unwanted := range []string{"snglcanvas.New("} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("canvas allocates per frame (%q):\n%s", unwanted, out.String())
		}
	}

	// Kitty image data is an APC escape that a cell-diffing renderer drops, so
	// the pixels must be transmitted out of band via tea.Raw — emitted from a
	// __canvasTransmit method wired into Init and Update — while View carries
	// only the placeholder grid (RenderTerminal). Lock that wiring in.
	got := out.String()
	for _, want := range []string{
		"func (m Model) __canvasTransmit() tea.Cmd",
		"tui.KittyTransmit(",
		"tea.Raw(",
		"m.__canvasTransmit()",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing out-of-band transmit wiring %q:\n%s", want, got)
		}
	}
	// The transmit call appears in both Init and Update (so the first paint and
	// reactive updates both transmit): method def + 2 call sites = 3 references.
	if n := strings.Count(got, "__canvasTransmit"); n < 3 {
		t.Errorf("expected __canvasTransmit referenced in Init and Update (>=3 occurrences), got %d", n)
	}
}
