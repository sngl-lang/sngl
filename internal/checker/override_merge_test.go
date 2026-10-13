package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

// Two packages each implementing one function for one target are two
// answers, however alike their bodies; one package reached twice is one.
func TestTwoPackagesEmptyFuncOverridesCollide(t *testing.T) {
	r := &mockResolver{pkgs: map[string]string{
		"base": "func ping() {}\n",
		"a":    "import base \"base\"\nimport \"sngl:platform/html\"\n\nfunc base.ping[html.platform] {}\n",
		"b":    "import base \"base\"\nimport \"sngl:platform/html\"\n\nfunc base.ping[html.platform] {}\n",
		"both": "import a \"a\"\n",
	}}
	langs, plats := testtargets.Targets()
	check := func(src string) []ir.Diagnostic {
		doc, err := parser.Parse("main.sngl", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		_, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Resolver: r})
		return diags
	}
	collides := func(diags []ir.Diagnostic) bool {
		for _, d := range diags {
			if d.Severity == ir.Error && strings.Contains(d.Msg, "has two implementations") {
				return true
			}
		}
		return false
	}
	if diags := check("import a \"a\"\nimport b \"b\"\n"); !collides(diags) {
		t.Errorf("two packages' empty overrides of one function merged: %v", diags)
	}
	if diags := check("import a \"a\"\nimport both \"both\"\n"); collides(diags) {
		t.Errorf("one package reached twice collided with itself: %v", diags)
	}
}
