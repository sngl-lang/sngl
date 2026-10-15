//go:build !js

package html

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"

	_ "duckfam.us/sngl/codegen/scheme/js"
)

// renderComponentHTML prepends the output block to src, compiles to HTML bytes,
// and returns the HTML string. Fails the test on any compile/check/lower/generate error.
func renderComponentHTML(t *testing.T, src string) string {
	t.Helper()

	fullSrc := "output { none { html() } }\n" + src

	fsys := fstest.MapFS{}

	doc, err := parser.Parse("app.sngl", []byte(fullSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	resolver := &asyncBrowserResolver{fsys: fsys}
	pkg, diags := checker.Check(doc, &checker.Config{
		FS:        fsys,
		Dir:       ".",
		IsMain:    true,
		Resolver:  resolver,
		Platforms: []ir.Platform{&Generator{}},
		// The js language is registered even though the target is `none`:
		// html's client script is JavaScript either way, so a page may name a
		// JavaScript identifier through sngl:language/js, and the checker can
		// only resolve that package for a language it knows.
		Languages: htmlLangs(),
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}

	lang := codegen.LookupLang("none")
	if lang == nil {
		t.Fatal("none language translator not registered")
	}

	gen := &Generator{}
	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), gen.PlatformIdentifier())
	// Mirror the CLI generate pipeline (cmd/sngl/pipeline.go): optimize →
	// lower → optimize. Wiring Platforms above + these passes is what makes
	// stdlib components render through their `platform html { }` bodies (the
	// real path), instead of the legacy renderStaticX helpers — so this test
	// reflects what `sngl generate` actually emits.
	optCfg := &optimize.Config{Platform: gen.PlatformIdentifier(), Language: lang.LanguageIdentifier(), Dir: "."}
	pkg = ir.ClonePackage(pkg)
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	if err := lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		t.Fatalf("optimize (post-lower): %v", err)
	}

	mem := codegen.NewMemSink()
	if err := gen.Generate(&codegen.Request{
		Doc:       doc,
		Pkg:       pkg,
		Lang:      lang,
		ProjectFS: fsys,
	}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}

	for name, content := range mem.Files() {
		if strings.HasSuffix(name, ".html") {
			return string(content)
		}
	}
	t.Fatal("expected at least 1 .html output file")
	return ""
}

// asyncBrowserResolver implements checker.ImportResolver backed by an
// in-memory FS, delegating scheme imports to the registered JS importer.
type asyncBrowserResolver struct {
	fsys fs.FS
}

func (r *asyncBrowserResolver) Resolve(_ fs.FS, importPath string) ([]*ast.Document, error) {
	entries, err := fs.ReadDir(r.fsys, importPath)
	if err != nil {
		return nil, fmt.Errorf("reading import dir %q: %w", importPath, err)
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		p := importPath + "/" + e.Name()
		data, err := fs.ReadFile(r.fsys, p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (r *asyncBrowserResolver) ResolveScheme(scheme, uri, _ string) (*ir.NativeImport, error) {
	imp := codegen.LookupScheme(scheme)
	if imp == nil {
		return nil, fmt.Errorf("scheme %q not supported", scheme)
	}
	if fsa, ok := imp.(codegen.FSAwareScheme); ok && r.fsys != nil {
		return fsa.ResolveFS(uri, r.fsys, ".")
	}
	return nil, fmt.Errorf("scheme %q requires OS filesystem; not supported in this test", scheme)
}

func (r *asyncBrowserResolver) ResolveSchemeFS(_, _, _ string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}
