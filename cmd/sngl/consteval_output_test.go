package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A compile-time value whose declared type says nothing — a Go func returning
// []any — carries its own type ref, and the declaration that resolves must
// still be there when Go is emitted. Reading the const through an ident used
// to go out through the interpreter's value model and come back as a naked
// struct literal, so the emitted field initializer was
// `[]any{struct{}{Name: "alpha", Value: 1}}`: not compilable Go, and not
// visible at the checker seam where the value is built.
//
// The assertion is on the emitted source for that reason. The value is read
// into a component var so it survives as an initializer rather than folding
// into the string it is measured by.
func TestDynConstKeepsItsDeclarationInEmittedGo(t *testing.T) {
	const src = `import . "sngl://std"
import lib "go://git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg"

const items = lib.Anything()

component main {
    var rows = items

    window #app(title="t") {
        text(value="n {string(rows.length)}")
    }
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

// repoRoot returns the directory of the go.mod above the test's own package.
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
