package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// generateGTK4ModelBuilt is the pipeline internal/build runs, which is what
// `sngl generate` is: optimize, lower, and optimize AGAIN. generateGTK4Model
// skips the optimizer entirely, which is fine for a claim about one emitted
// statement and not fine for one about whether the file compiles -- an
// unoptimized emission still carries `null` where no Go declares one.
func generateGTK4ModelBuilt(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		IsMain:    true,
		Platforms: codegen.CollectPlatforms(),
		Languages: []ir.Language{lang},
		Targets:   []ir.StaticTarget{{Platform: "gtk4", Language: "go"}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check diag: %s: %s", d.Pos, d.Msg)
		}
	}
	optCfg := &optimize.Config{Platform: "gtk4", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	g := &Generator{}
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
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

// buildGeneratedGo type-checks generated Go by compiling it. gtk4 builds rather
// than runs: constructing a widget needs a display, and what these tests are
// about is the types.
//
// The temp dir is created UNDER this package so gtk4rt resolves through the
// repo's own go.mod; a dir in os.TempDir() has no module above it. The
// emission is `package main` without a main(), which links only under `sngl
// run`'s own scaffold, so one is supplied here.
func buildGeneratedGo(t *testing.T, prefix, model string) {
	t.Helper()
	if out := compileErrors(t, prefix, model); out != "" {
		t.Errorf("generated Go does not compile\n--- go build ---\n%s\n--- model.go ---\n%s", out, model)
	}
}

// compileErrors is every diagnostic `go build` reports for the emitted file,
// or "" when it compiles. -gcflags=-e so the list is not truncated at ten.
//
// Named separately because a stock fixture may hold several defects at once: a
// claim that one class of them is gone is made against this, by asking what
// the compiler still says, rather than against a whole-file compile that some
// unrelated defect keeps failing.
func compileErrors(t *testing.T, prefix, model string) string {
	t.Helper()
	tmp, err := os.MkdirTemp(".", prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "entry.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-gcflags=-e", ".")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	return string(out)
}
