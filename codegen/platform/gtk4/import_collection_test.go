package gtk4

import (
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// Regression: an import (here "fmt", pulled in by string interpolation) that is
// first required while rendering a *computed* method body must still appear in
// model.go's import block. The body is rendered inside newTemplateData after
// the import set was sampled, so the sample has to happen last. Without the
// fix the generated file uses fmt.Sprint but never imports fmt.
func TestIntegration_ComputedBodyImportCollected(t *testing.T) {
	skipWithoutGIR(t)
	src := `
import . "sngl:ui"
window {
    var n = 3
    func double() => n * 2
    func summary() string {
        var d = double()
        return "d={d}"
    }
    text(value=summary)
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
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := string(mem.Files()["model.go"])

	if !strings.Contains(out, "fmt.Sprint") {
		t.Fatalf("expected the computed body to use fmt.Sprint\n--- generated ---\n%s", out)
	}
	// The import line is "\t\"fmt\"" inside the import block; this is distinct
	// from the fmt.Sprint usage in the body.
	if !strings.Contains(out, "\t\"fmt\"") {
		t.Errorf("model.go uses fmt.Sprint but does not import fmt\n--- generated ---\n%s", out)
	}
}
