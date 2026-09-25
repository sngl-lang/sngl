package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A value from a Go func returning []any carries its own type ref, and reading
// the const through an ident used to lose it in the interpreter's value model:
// the emitted initializer came back as `[]any{struct{}{Name: "alpha"...}}`,
// which is not compilable Go. Only the emitted source shows that, hence the
// assertion on it — and the value is read into a var so it survives as an
// initializer rather than folding into the string measuring it. The var is the
// package's because the window is: nothing here declares a root component, so
// there is no splice to rename the declaration and the assertion can name it.
func TestDynConstKeepsItsDeclarationInEmittedGo(t *testing.T) {
	const src = `import . "sngl:ui"
import . "sngl:ui"
import lib "go:git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg"

const items = lib.Anything()

var rows = items

window #app(title="t") {
    text(value="n {string(rows.length)}")
}
`
	// The evaluator program is generated beside the source and built against
	// the enclosing module, so the source has to sit inside this repo: a
	// t.TempDir() is in no module that provides pkg/go/consteval.
	root := repoRoot(t)
	dir, err := os.MkdirTemp(root, ".sngl-consteval-out-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	app := filepath.Join(dir, "app.sngl")
	if err := os.WriteFile(app, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out")
	resetFlags(rootCmd)
	rootCmd.SetArgs([]string{"generate", "--platform=bubbletea", "--lang=go", "--out=" + out, app})
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(out, "model.go"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `[]any{purepkg.Item{Name: "alpha", Value: 1}}`
	if !strings.Contains(string(got), want) {
		t.Errorf("model.go does not initialize rows with %s:\n%s", want, got)
	}
	// The value survived, so the package it names has to be imported for it.
	if !goImports(t, out)[purepkgPath] {
		t.Errorf("model.go spells purepkg.Item without importing %s:\n%s", purepkgPath, got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	mod := findGoModAncestor(cwd)
	if mod == "" {
		t.Skip("no enclosing module")
	}
	return filepath.Dir(mod)
}

const purepkgPath = "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg"

// An import is a Go import only where the emitted code names it. A `file:`
// import's path is a directory on disk rather than a Go package, and a `go:`
// import whose every call folded at build time is named by nothing, which Go
// refuses as an unused import -- so neither may reach the import block.
func TestFoldedImportsLeaveNoGoImport(t *testing.T) {
	const src = `import . "sngl:ui"
import lib "go:git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg"
import "file:public"

const greeting = lib.Greet("sngl")
const logo = public.path("logo.txt")

window #app(title="t") {
    text(value=greeting)
    image(src=logo)
}
`
	root := repoRoot(t)
	dir, err := os.MkdirTemp(root, ".sngl-folded-imports-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public", "logo.txt"), []byte("logo"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "app.sngl")
	if err := os.WriteFile(app, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, platform := range []string{"bubbletea", "fyne", "gtk4"} {
		t.Run(platform, func(t *testing.T) {
			out := filepath.Join(dir, "out-"+platform)
			resetFlags(rootCmd)
			rootCmd.SetArgs([]string{"generate", "--platform=" + platform, "--lang=go", "--out=" + out, app})
			rootCmd.SilenceUsage = true
			rootCmd.SilenceErrors = true
			if err := rootCmd.Execute(); err != nil {
				t.Fatalf("generate: %v", err)
			}
			imports := goImports(t, out)
			if imports[purepkgPath] {
				t.Errorf("imports %s, whose only call folded", purepkgPath)
			}
			for path := range imports {
				if strings.HasSuffix(path, "public") {
					t.Errorf("imports %q, a file: directory", path)
				}
			}
		})
	}
}

// goImports is every import path of the Go files directly in dir.
func goImports(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	paths := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			paths[p] = true
		}
	}
	return paths
}
