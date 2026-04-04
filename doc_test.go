package sngl_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
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
				if block.annotation == "nocheck" {
					return
				}

				src := block.prelude

				switch block.annotation {
				case "component":
					src += "component main {\n" + block.source + "\n}"
				case "expression":
					src += "component main {\n  computed _x = " + strings.TrimSpace(block.source) + "\n}"
				default:
					src += block.source
				}

				doc, err := parser.Parse(name, strings.NewReader(src))
				if err != nil {
					t.Errorf("parse error at %s line %d:\n%s\n---\n%v", file, block.line, src, err)
					return
				}

				// Type check
				isMain := doc.App != nil
				if err := checker.Check(doc, ".", checker.DefaultResolver(), nil, nil, isMain); err != nil {
					t.Errorf("type error at %s line %d:\n%s\n---\n%v", file, block.line, src, err)
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
	prelude    string // optional SNGL source from HTML comment body
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
	var annotation string
	var prelude string

	// Store all lines so we can look backward from ```sngl
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	for lineNum = 0; lineNum < len(lines); lineNum++ {
		line := lines[lineNum]
		if strings.TrimSpace(line) == "```sngl" {
			inBlock = true
			blockStart = lineNum + 2 // 1-based, next line
			current.Reset()
			annotation = ""
			prelude = ""

			// Look backward to find <!-- SNGL-... comment
			annotation, prelude = findAnnotation(lines, lineNum)
			continue
		}
		if inBlock && strings.TrimSpace(line) == "```" {
			inBlock = false
			blocks = append(blocks, snglBlock{
				source:     current.String(),
				line:       blockStart,
				annotation: annotation,
				prelude:    prelude,
			})
			continue
		}
		if inBlock {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	return blocks
}

// findAnnotation looks backward from fenceLine to find a <!-- SNGL-... --> comment.
// It returns the annotation type and any prelude source from a multi-line comment.
func findAnnotation(lines []string, fenceLine int) (annotation, prelude string) {
	// Look at the line immediately before the fence
	if fenceLine == 0 {
		return "", ""
	}

	// First check: is the previous line a single-line annotation?
	prev := strings.TrimSpace(lines[fenceLine-1])
	if strings.HasPrefix(prev, "<!-- SNGL-") && strings.HasSuffix(prev, "-->") {
		// Single-line: <!-- SNGL-component -->
		inner := strings.TrimPrefix(prev, "<!-- SNGL-")
		inner = strings.TrimSuffix(inner, "-->")
		inner = strings.TrimSpace(inner)
		return inner, ""
	}

	// Check for multi-line: the line before the fence should be "-->"
	if prev != "-->" {
		return "", ""
	}

	// Scan backward to find the opening <!-- SNGL-
	var preludeLines []string
	for i := fenceLine - 2; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "<!-- SNGL-") {
			// Found the opening line
			inner := strings.TrimPrefix(trimmed, "<!-- SNGL-")
			// The annotation is the first word/token
			inner = strings.TrimSpace(inner)
			// If there's content after the annotation on this line
			if idx := strings.IndexByte(inner, ' '); idx >= 0 {
				annotation = inner[:idx]
				// Rest of first line is also prelude
				preludeLines = append([]string{inner[idx+1:]}, preludeLines...)
			} else if strings.HasSuffix(inner, "-->") {
				// Shouldn't happen since we already checked single-line
				annotation = strings.TrimSuffix(inner, "-->")
				annotation = strings.TrimSpace(annotation)
			} else {
				annotation = inner
			}
			var b strings.Builder
			for _, pl := range preludeLines {
				b.WriteString(pl)
				b.WriteByte('\n')
			}
			return annotation, b.String()
		}
		preludeLines = append([]string{lines[i]}, preludeLines...)
	}
	return "", ""
}
