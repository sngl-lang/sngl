package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestHtmlDirectiveSurvivesWithPlatformActive is the faithful regression guard
// for the html.frontend/html.backend placement directives (GitLab #27). Unlike
// the checker-package unit test, it registers the html platform via
// Config.Platforms — the path the `sngl` CLI uses — so the platform's own
// "html" raw-element namespace (html.div, …) coexists with the stdlib "html"
// namespace that owns the directives. The directive must still resolve AND
// survive optimize+lower carrying Func.Intrinsic == "HtmlFrontend".
func TestHtmlDirectiveSurvivesWithPlatformActive(t *testing.T) {
	src := `
import . "sngl://std"
output { js { html } }
component main {
    var n = 0
    text(value="x {html.frontend(n)}")
    html.div { text(value="raw element still works") }
}`
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gen := &Generator{}
	lang := codegen.LookupLang("none")
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: []ir.Platform{gen},
		Languages: []ir.Language{lang},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	caps := gen.Capabilities(lang).ToLowerCaps()
	if err := optimize.Optimize(pkg, &optimize.Config{
		Platform: gen.PlatformIdentifier(),
		Language: lang.LanguageIdentifier(),
	}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) error {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == "HtmlFrontend" {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	if !found {
		t.Fatal("html.frontend call was erased; must survive as an intrinsic for placement analysis")
	}
}
