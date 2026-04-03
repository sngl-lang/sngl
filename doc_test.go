package sngl_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// TestDocSNGLBlocks finds all ```sngl code blocks in markdown files and
// verifies they parse correctly and produce stable formatting.
func TestDocSNGLBlocks(t *testing.T) {
	var files []string
	filepath.WalkDir("docs", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})

	for _, file := range files {
		blocks := extractSNGLBlocks(t, file)
		for i, block := range blocks {
			name := fmt.Sprintf("%s#%d", file, i+1)
			t.Run(name, func(t *testing.T) {
				src := block.source

				switch block.annotation {
				case "component":
					src = "component main {\n" + src + "\n}"
				case "expression":
					src = "component main {\n  computed _x = " + strings.TrimSpace(src) + "\n}"
				}

				doc, err := parser.Parse(name, strings.NewReader(src))
				if err != nil {
					t.Errorf("parse error at %s line %d:\n%s\n---\n%v", file, block.line, src, err)
					return
				}

				// Round-trip format check: format and re-parse to verify stability
				formatted := parser.Format(doc)
				_, err = parser.Parse(name+".fmt", strings.NewReader(formatted))
				if err != nil {
					t.Errorf("formatted output doesn't re-parse at %s line %d:\n%s\n---\n%v", file, block.line, formatted, err)
				}
			})
		}
	}
}

type snglBlock struct {
	source     string
	line       int
	annotation string // "", "component", "expression"
}

func extractSNGLBlocks(t *testing.T, path string) []snglBlock {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var blocks []snglBlock
	scanner := bufio.NewScanner(f)
	lineNum := 0
	inBlock := false
	var current strings.Builder
	blockStart := 0
	prevLine := ""
	var annotation string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if strings.TrimSpace(line) == "```sngl" {
			inBlock = true
			blockStart = lineNum + 1
			current.Reset()
			annotation = ""
			if strings.Contains(prevLine, "<!-- SNGL-component -->") {
				annotation = "component"
			} else if strings.Contains(prevLine, "<!-- SNGL-expression -->") {
				annotation = "expression"
			}
			continue
		}
		if inBlock && strings.TrimSpace(line) == "```" {
			inBlock = false
			blocks = append(blocks, snglBlock{source: current.String(), line: blockStart, annotation: annotation})
			continue
		}
		if inBlock {
			current.WriteString(line)
			current.WriteByte('\n')
		}
		prevLine = line
	}
	return blocks
}
