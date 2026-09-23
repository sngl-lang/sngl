package main

import (
	"os"
	"path/filepath"
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
