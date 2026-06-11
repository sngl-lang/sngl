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
	src := `component main {
    canvas(width=40px, height=20px) {
        rect(x=0.0, y=0.0, w=40.0, h=20.0, style=CanvasStyle{fill=color{r=10, g=20, b=30, a=255}}) {}
    }
}`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
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
	out := ""
	for _, f := range mem.Files() {
		out += string(f)
	}
	for _, want := range []string{"snglcanvas.New(40, 20)", "ctx.Rect(", "RenderTerminal("} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in emitted bubbletea output:\n%s", want, out)
		}
	}
}
