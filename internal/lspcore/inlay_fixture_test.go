package lspcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testutil"
)

func TestInlayFixtures(t *testing.T) {
	matches, err := filepath.Glob("../../testdata/lsp_inlay/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no inlay fixtures found")
	}
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, perr := parser.Parse(filepath.Base(path), src)
			if perr != nil {
				t.Fatalf("parse: %v", perr)
			}
			dirs, err := testutil.ParseInlayDirectives(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(dirs) == 0 {
				t.Fatal("no INLAY directives")
			}
			// Compute hints across the full document (no range filter).
			full := fullDocumentRange(string(src))
			got := ComputeInlayHints(string(src), doc, full)

			// Index hints by position.
			byPos := make(map[Position]InlayHintResult)
			for _, h := range got {
				byPos[h.Position] = h
			}
			for _, d := range dirs {
				// Hint should anchor at end of target (line, col+TargetLen).
				anchor := Position{Line: d.Line - 1, Character: (d.Col - 1) + d.TargetLen}
				h, ok := byPos[anchor]
				if d.Negate {
					if ok {
						t.Errorf("INLAY-NOT(%s) but got hint %q at %v (directive line %d)",
							d.Target, h.Label, anchor, d.DirLine)
					}
					continue
				}
				if !ok {
					t.Errorf("INLAY(%s) expected hint %q at %v (directive line %d) — no hint there. Hints: %+v",
						d.Target, d.Label, anchor, d.DirLine, got)
					continue
				}
				if h.Label != d.Label {
					t.Errorf("INLAY(%s) label = %q, want %q (at %v, directive line %d)",
						d.Target, h.Label, d.Label, anchor, d.DirLine)
				}
			}
		})
	}
}

// fullDocumentRange returns a Range covering the entire document.
func fullDocumentRange(content string) Range {
	lines := strings.Split(content, "\n")
	return Range{
		Start: Position{Line: 0, Character: 0},
		End:   Position{Line: len(lines), Character: 0},
	}
}
