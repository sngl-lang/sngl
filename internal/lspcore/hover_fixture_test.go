package lspcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestHoverFixtures(t *testing.T) {
	matches, err := filepath.Glob("../../testdata/lsp_hover/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no hover fixtures found")
	}
	for _, path := range matches {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, perr := parser.Parse(filepath.Base(path), src)
			if perr != nil {
				t.Fatalf("parse %s: %v", path, perr)
			}
			dirs, err := testutil.ParseHoverDirectives(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(dirs) == 0 {
				t.Fatal("no HOVER directives in fixture")
			}
			for _, d := range dirs {
				got := HoverAt(string(src), doc, d.Line, d.Col)
				if d.Negate {
					if strings.Contains(got, d.Substring) {
						t.Errorf("HOVER-NOT(%s) %q matched at %d:%d (directive line %d)\n--- got ---\n%s",
							d.Target, d.Substring, d.Line, d.Col, d.DirLine, got)
					}
					continue
				}
				if !strings.Contains(got, d.Substring) {
					t.Errorf("HOVER(%s) missing %q at %d:%d (directive line %d)\n--- got ---\n%s",
						d.Target, d.Substring, d.Line, d.Col, d.DirLine, got)
				}
			}
		})
	}
}
