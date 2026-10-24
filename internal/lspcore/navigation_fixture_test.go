package lspcore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

func TestNavigationFixtures(t *testing.T) {
	matches, err := filepath.Glob("../../testdata/lsp_definition/*.sngl")
	if err != nil || len(matches) == 0 {
		t.Fatalf("no navigation fixtures: %v", err)
	}
	for _, rel := range matches {
		path, _ := filepath.Abs(rel)
		t.Run(strings.TrimSuffix(filepath.Base(path), ".sngl"), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := parser.Parse(path, src)
			if err != nil {
				t.Fatal(err)
			}
			pkg, diags := checker.CheckPackage([]*ast.Document{doc}, &checker.Config{Dir: filepath.Dir(path), IsMain: true})
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Fatalf("fixture does not check: %v", d)
				}
			}
			sources := func(f string) (string, bool) {
				if f == path {
					return string(src), true
				}
				return LibrarySource(f)
			}
			ix := BuildIndex(pkg, []string{path}, sources)
			dirs, err := testutil.ParseNavDirectives(path, LibrarySource)
			if err != nil {
				t.Fatal(err)
			}
			if len(dirs) == 0 {
				t.Fatal("no DEFINITION or REFERENCES directives")
			}
			for _, d := range dirs {
				r := ix.At(path, d.Line, d.Col)
				if r == nil {
					t.Errorf("line %d: %s(%s): no name at %d:%d", d.DirLine, d.Kind, d.Target, d.Line, d.Col)
					continue
				}
				switch d.Kind {
				case "DEFINITION":
					site, ok := ix.Sites[r.Decl]
					wantFile := path
					if d.File != "" {
						wantFile = d.File
					}
					got := ast.Pos{File: site.Pos.File, Line: site.Pos.Line, Column: site.Pos.Column}
					want := ast.Pos{File: wantFile, Line: d.WantLine, Column: d.WantCol}
					if !ok || got != want {
						t.Errorf("line %d: DEFINITION(%s) = %v, want %v", d.DirLine, d.Target, got, want)
					}
				case "REFERENCES":
					var got [][2]int
					for _, ref := range ix.RefsTo(r.Decl) {
						got = append(got, [2]int{ref.Pos.Line, ref.Pos.Column})
					}
					want := slices.Clone(d.WantRefs)
					slices.SortFunc(want, func(a, b [2]int) int {
						if a[0] != b[0] {
							return a[0] - b[0]
						}
						return a[1] - b[1]
					})
					if !slices.Equal(got, want) {
						t.Errorf("line %d: REFERENCES(%s) = %v, want %v", d.DirLine, d.Target, got, want)
					}
				}
			}
		})
	}
}
