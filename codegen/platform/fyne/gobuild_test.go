package fyne

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
)

// generateFyneModelBuilt is the pipeline internal/build runs, which is what
// `sngl generate` is: optimize, lower, and optimize AGAIN. generateFyneModel
// skips the optimizer entirely, and an unoptimized emission is a different
// program -- a claim about a stock fixture has to be made against this one.
func generateFyneModelBuilt(t *testing.T, src string) string {
	t.Helper()
	pkg := checkForFyne(t, src)
	lang := codegen.LookupLang("go")
	if lang == nil {
		t.Fatal("go lang not registered")
	}
	g := &Generator{}
	optCfg := &optimize.Config{Platform: "fyne", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "fyne", Language: "go"}); err != nil {
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
// The temp dir is created UNDER this package so fyne.io/fyne/v2 and the sngl
// runtime packages resolve through the repo's own go.mod; a dir in
// os.TempDir() has no module above it.
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
	if err == nil {
		return ""
	}
	return string(out)
}
