package gtk4

import (
	"os"
	"path/filepath"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// genOptions is the options the generate helpers hand Generate, which
// withCgo sets.
var genOptions map[string]any

// withCgo builds what the rest of t generates under the cgo option: the
// registered platform the check reads the widget declarations from, and the
// generator. A program needs it to take the cgo path when it renders nothing
// that puts it there of its own accord, a canvas or a #[cnative] it calls.
// The GIR a test selected before it stays selected.
func withCgo(t *testing.T) {
	t.Helper()
	reg := codegen.LookupPlatform(platformName).(*Generator)
	if err := reg.Configure(map[string]string{"gir": reg.girOption(), "cgo": "true"}); err != nil {
		t.Fatalf("configure registered gtk4: %v", err)
	}
	t.Cleanup(func() { _ = reg.Configure(map[string]string{}) })
	genOptions = map[string]any{"cgo": true}
	t.Cleanup(func() { genOptions = nil })
}

func genRequest(pkg *ir.Package, lang codegen.LangTranslator) *codegen.Request {
	req := &codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}
	if genOptions != nil {
		req.Options = codegen.OptionsFromMap(genOptions)
	}
	return req
}

// generateGTK4ModelBuilt is the pipeline internal/build runs, which is what
// `sngl generate` is: optimize, lower, and optimize AGAIN. Without the
// optimizer an emission carries `null` where no Go declares one, and names
// library structs and consts only the optimizer declares.
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
	pkg = ir.ClonePackageFor(pkg, "gtk4", "go")
	optCfg := &optimize.Config{Platform: "gtk4", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	g := &Generator{}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize2: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(genRequest(pkg, lang), mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	modelSrc, ok := mem.Files()["model.go"]
	if !ok {
		t.Fatalf("model.go not among generated files %v", mem.Files())
	}
	return string(modelSrc)
}

// generateGTK4FilesBuilt is generateGTK4ModelBuilt keeping every emitted file.
// model.go alone compiles for most programs, but not for one holding a canvas:
// the cairo trampoline registry it appends to is declared in callbacks.go.
func generateGTK4FilesBuilt(t *testing.T, src string) map[string]string {
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
	pkg = ir.ClonePackageFor(pkg, "gtk4", "go")
	optCfg := &optimize.Config{Platform: "gtk4", Language: "go"}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	g := &Generator{}
	if err := lower.Lower(pkg, codegen.CapsOrNone(lang.LanguageIdentifier(), g.PlatformIdentifier()), lower.Options{Platform: "gtk4", Language: "go"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize2: %v", err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(genRequest(pkg, lang), mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := map[string]string{}
	for name, b := range mem.Files() {
		out[name] = string(b)
	}
	if _, ok := out["model.go"]; !ok {
		t.Fatalf("model.go not among generated files %v", mem.Files())
	}
	return out
}

// buildGeneratedFiles compiles a whole emitted package, the way
// buildGeneratedGo compiles model.go alone.
func buildGeneratedFiles(t *testing.T, prefix string, files map[string]string) {
	t.Helper()
	build := goFiles(files)
	build["entry.go"] = "package main\n\nfunc main() {}\n"
	if out, ok := runGo(t, prefix, build, false, "build", "-gcflags=-e", "."); !ok {
		t.Errorf("generated Go does not compile\n--- go build ---\n%s\n--- model.go ---\n%s", out, files["model.go"])
	}
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
	files := map[string]string{"model.go": model, "entry.go": "package main\n\nfunc main() {}\n"}
	if out, ok := runGo(t, prefix, files, false, "build", "-gcflags=-e", "."); !ok {
		return out
	}
	return ""
}
