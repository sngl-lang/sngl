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

const purepkgPath = "duckfam.us/sngl/internal/optimize/testdata/purepkg"

// An import is a Go import only where the emitted code names it. A `file:`
// import's path is a directory on disk rather than a Go package, and a `go:`
// import whose every call folded at build time is named by nothing, which Go
// refuses as an unused import -- so neither may reach the import block.
func TestFoldedImportsLeaveNoGoImport(t *testing.T) {
	const src = `import . "sngl:ui"
import lib "go:duckfam.us/sngl/internal/optimize/testdata/purepkg"
import "file:public"

const greeting = lib.Greet("sngl")
const logo = public.path("logo.txt")

window #app(title="t") {
    text(value=greeting)
    image {
        file(path=logo)
    }
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
			rootCmd.SetArgs([]string{"generate", "--allow-eval=go:" + purepkgPath, "--platform=" + platform, "--lang=go", "--out=" + out, app})
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
