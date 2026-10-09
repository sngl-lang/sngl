package html

import (
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// TestMainElementNotSwallowedByMainComponent guards a regression where a
// block-form `html.main { ... }` element was rendered as nothing: the html
// element name "main" collided with the entry `component main`, so codegen
// re-resolved the bare name to the user component and dropped the element and
// all of its children. Other tag names (section, div, ...) were unaffected
// only because no same-named component existed. The whole docs site rendered
// the sidebar but lost every page body, which lived inside <main>.
func TestMainElementNotSwallowedByMainComponent(t *testing.T) {
	src := `
import . "sngl:ui"
import "sngl:platform/html"
output { none { html() } }
window(title="Home") {
    html.div {
        html.main {
            html.p(innerText="BODY CONTENT")
        }
    }
}
`
	out := generateMainPage(t, src)

	if !strings.Contains(out, "<main") {
		t.Errorf("html.main element dropped — no <main> tag in output\n--- generated ---\n%s", out)
	}
	if !strings.Contains(out, "BODY CONTENT") {
		t.Errorf("html.main children dropped — body content missing\n--- generated ---\n%s", out)
	}
}

// generateMainPage runs the full pipeline for the html/none target with the
// html platform wired in (so html.* native element names resolve) and returns
// the generated index page.
func generateMainPage(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gen := &Generator{}
	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{gen},
		Languages: htmlLangs(),
		Targets:   []ir.StaticTarget{{Platform: gen.PlatformIdentifier(), Language: lang.LanguageIdentifier()}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}
	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), gen.PlatformIdentifier())
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: gen.PlatformIdentifier(),
		Language: lang.LanguageIdentifier(),
	}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{Doc: doc, Pkg: pkg, Lang: lang}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content)
		}
	}
	t.Fatal("expected at least 1 .html file")
	return ""
}
