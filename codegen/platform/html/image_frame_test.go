package html

import (
	"regexp"
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

func generatePageWith(t *testing.T, src string, opts map[string]any) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gen := &Generator{}
	lang := codegen.LookupLang("none")
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{gen},
		Languages: htmlLangs(),
		Targets:   []ir.StaticTarget{{Platform: gen.PlatformIdentifier(), Language: lang.LanguageIdentifier()}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}
	pkg = ir.ClonePackage(pkg)
	if err := optimize.Optimize(pkg, &optimize.Config{Platform: gen.PlatformIdentifier(), Language: lang.LanguageIdentifier()}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), gen.PlatformIdentifier()), lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{Doc: doc, Pkg: pkg, Lang: lang, Options: codegen.OptionsFromMap(opts)}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out, ok := mem.Files()["index.html"]
	if !ok {
		t.Fatalf("no index.html among %v", mem.Files())
	}
	return string(out)
}

const imageFrameSrc = `
import ui "sngl:ui"

ui.window {
    ui.image(alt="nothing yet", style={width=40px, height=40px})
}
`

// A test build writes the frame's class once: the test-mode attributes wrote
// it again beside the one every build writes.
func TestATestBuildWritesAFramesClassOnce(t *testing.T) {
	out := generatePageWith(t, imageFrameSrc, map[string]any{"test": true})
	for _, tag := range regexp.MustCompile(`<span[^>]*sngl-image[^>]*>`).FindAllString(out, -1) {
		if n := strings.Count(tag, " class="); n != 1 {
			t.Errorf("class written %d times: %s", n, tag)
		}
	}
}

// A frame with no source is clipped as one with a source is: the rule was
// added only where a source's <img> was written.
func TestAFrameWithNoSourceIsClipped(t *testing.T) {
	out := generatePageWith(t, imageFrameSrc, nil)
	if !strings.Contains(out, `class="sngl-image`) {
		t.Fatalf("no frame:\n%s", out)
	}
	if !strings.Contains(out, ".sngl-image { overflow: hidden; }") {
		t.Errorf("the frame's clip rule is missing:\n%s", out)
	}
}
