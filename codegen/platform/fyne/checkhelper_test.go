package fyne

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkForFyne parses and checks src the way a real `--platform fyne` build
// does: with this platform in the checker's Config.
//
// The platform is not optional. mergePlatformExtensions is what collects
// `component sngl.X { ... }` in fyne.sngl into each stdlib component's
// PlatformOverrides, and it returns immediately when Config.Platforms is empty.
// A test that omits it leaves every stdlib component with an empty body, so
// nodes reach codegen under their stdlib names, match no widget, and are
// dropped — while their AppendChild is still emitted. That is issue #120's
// shape, and a test harness must not be the thing producing it.
func checkForFyne(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := codegen.LookupPlatform("fyne")
	if p == nil {
		t.Fatal("fyne platform not registered")
	}
	cfg := &checker.Config{IsMain: true, Platforms: []ir.Platform{p}, Targets: []ir.StaticTarget{{Platform: "fyne", Language: "go"}}}
	if l := codegen.LookupLang("go"); l != nil {
		cfg.Languages = []ir.Language{l}
	}
	pkg, diags := checker.Check(doc, cfg)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s: %s", d.Pos, d.Msg)
		}
	}
	return pkg
}
