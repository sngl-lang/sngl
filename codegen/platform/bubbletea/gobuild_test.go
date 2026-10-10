package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// generateBubbleteaModel is the pipeline internal/build runs, which is what
// `sngl generate` is: optimize, lower, and optimize AGAIN. The second pass is
// the one that matters here -- it inlines a recursive component's residual
// instantiation another eight levels, after the passes that gave that
// component its props -- so a claim about the emitted program cannot be made
// against a single optimize.
func generateBubbleteaModel(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var plats []ir.Platform
	if p := codegen.LookupPlatform("bubbletea"); p != nil {
		plats = append(plats, p)
	}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: plats,
		Languages: []ir.Language{lang},
		Targets:   []ir.StaticTarget{{Platform: "bubbletea", Language: "go"}},
	})
	if hasErrors(diags) {
		t.Fatalf("check: %s", firstError(diags))
	}
	optCfg := &optimize.Config{Platform: "bubbletea", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	g := &Generator{}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "bubbletea", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize2: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	modelSrc, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatalf("model.go not among generated files %v", mem.Files())
	}
	return string(modelSrc)
}

// fixtureSource reads a root testdata fixture. A defect that only stock
// fixtures show is one a hand-written program in this file would not have.
func fixtureSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// buildGeneratedGo type-checks generated Go by compiling it.
//
// The temp dir is created UNDER this package so charm.land/bubbletea and the
// sngl runtime packages resolve through the repo's own go.mod; a dir in
// os.TempDir() has no module above it.
func buildGeneratedGo(t *testing.T, prefix, model string) {
	t.Helper()
	tmp, err := os.MkdirTemp(".", "_"+prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-gcflags=-e", ".")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("generated Go does not compile: %v\n--- go build ---\n%s\n--- model.go ---\n%s", err, out, model)
	}
}
