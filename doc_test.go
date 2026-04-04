package sngl_test

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	sngl "git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"golang.org/x/tools/txtar"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// TestDocSNGLBlocks finds all ```sngl code blocks in markdown files and
// verifies they parse correctly and produce stable formatting.
func TestDocSNGLBlocks(t *testing.T) {
	t.Skip()
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
				var checkFS fs.FS = os.DirFS(".")

				// If prelude contains txtar markers, build an in-memory FS.
				var replacements []string
				if strings.Contains(block.prelude, "-- ") && strings.Contains(block.prelude, " --") {
					ar := txtar.Parse([]byte(block.prelude))
					src = string(ar.Comment) // non-file prelude content
					mapFS := fstest.MapFS{}
					for _, f := range ar.Files {
						if f.Name == "..." {
							replacements = append(replacements, strings.TrimSpace(string(f.Data)))
							continue
						}
						mapFS[f.Name] = &fstest.MapFile{Data: f.Data}
					}
					checkFS = mapFS
				}

				switch block.annotation {
				case "component":
					src += "component main {\n" + block.source + "\n}"
				case "expression":
					src += "component main {\n  computed _x = " + strings.TrimSpace(block.source) + "\n}"
				default:
					src += block.source
				}

				// Replace each "..." in source with the next -- ... -- file content.
				for _, r := range replacements {
					src = strings.Replace(src, "...", r, 1)
				}

				doc, err := parser.Parse(name, strings.NewReader(src))
				if err != nil {
					t.Errorf("parse error at %s line %d:\n%s\n---\n%v", file, block.line, src, err)
					return
				}

				// Type check
				isMain := doc.App != nil
				if err := checker.Check(doc, checkFS, "", checker.DefaultResolver(), sngl.DefaultSchemeResolver(), sngl.BuildAPIConfig(doc), isMain); err != nil {
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

// TestDocSNGLFormat checks that all ```sngl code blocks in docs are properly formatted.
// Set SNGL_FMT_DOCS=1 to write formatted snippets back into the markdown files.
func TestDocSNGLFormat(t *testing.T) {
	t.Skip()
	writeBack := os.Getenv("SNGL_FMT_DOCS") == "1"

	var mdFiles []string
	filepath.WalkDir("docs", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(path, ".md") {
			mdFiles = append(mdFiles, path)
		}
		return nil
	})

	for _, file := range mdFiles {
		blocks := extractSNGLBlocks(t, file)
		if len(blocks) == 0 {
			continue
		}

		var edits []edit

		for i, block := range blocks {
			name := fmt.Sprintf("%s#%d", file, i+1)
			t.Run(name, func(t *testing.T) {
				if block.annotation == "nocheck" || block.annotation == "expression" {
					return
				}

				src := block.prelude
				hasReplacements := false

				if strings.Contains(block.prelude, "-- ") && strings.Contains(block.prelude, " --") {
					ar := txtar.Parse([]byte(block.prelude))
					src = string(ar.Comment)
					for _, f := range ar.Files {
						if f.Name == "..." {
							hasReplacements = true
						}
					}
				}

				switch block.annotation {
				case "component":
					src += "component main {\n" + block.source + "\n}"
				default:
					src += block.source
				}

				doc, err := parser.Parse(name, strings.NewReader(src))
				if err != nil {
					return // parse errors caught by TestDocSNGLBlocks
				}

				formatted := parser.Format(doc)

				// Unwrap: extract the snippet portion from the formatted output
				var snippet string
				switch block.annotation {
				case "component":
					snippet = unwrapComponent(formatted, block.prelude)
				default:
					// Top-level: strip the prelude portion
					if block.prelude != "" {
						fmtPrelude := formatted
						// Re-parse just the prelude to get its formatted length
						preDoc, perr := parser.Parse("prelude", strings.NewReader(block.prelude))
						if perr == nil {
							fmtPre := parser.Format(preDoc)
							if strings.HasPrefix(formatted, fmtPre) {
								fmtPrelude = formatted[len(fmtPre):]
							}
						}
						snippet = fmtPrelude
					} else {
						snippet = formatted
					}
				}

				// Normalize: trim trailing whitespace from both
				original := strings.TrimRight(block.source, " \t\n")
				snippet = strings.TrimRight(snippet, " \t\n")

				if original != snippet {
					if hasReplacements {
						// Has ... replacements — check only, don't write back
						t.Errorf("formatting differs (has ... replacement, not auto-fixable):\n--- original ---\n%s\n--- formatted ---\n%s", original, snippet)
					} else if writeBack {
						edits = append(edits, edit{
							startLine: block.line,
							source:    block.source,
							formatted: snippet + "\n",
						})
					} else {
						t.Errorf("formatting differs (run with SNGL_FMT_DOCS=1 to fix):\n--- original ---\n%s\n--- formatted ---\n%s", original, snippet)
					}
				}
			})
		}

		// Apply edits to the file
		if writeBack && len(edits) > 0 {
			applyDocEdits(t, file, edits)
		}
	}
}

// unwrapComponent extracts the inner body from a formatted "component main { ... }"
// and un-indents by one level (4 spaces).
func unwrapComponent(formatted, prelude string) string {
	// Strip prelude from formatted output
	src := formatted
	if prelude != "" {
		preDoc, err := parser.Parse("prelude", strings.NewReader(prelude))
		if err == nil {
			fmtPre := parser.Format(preDoc)
			src = strings.TrimPrefix(formatted, fmtPre)
		}
	}

	lines := strings.Split(src, "\n")
	// Find "component main {" and closing "}"
	start := -1
	end := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if start == -1 && strings.HasPrefix(trimmed, "component main") && strings.HasSuffix(trimmed, "{") {
			start = i + 1
			continue
		}
		if start != -1 && trimmed == "}" {
			end = i
			break
		}
	}
	if start == -1 || end == -1 {
		return src
	}

	// Un-indent inner lines by 4 spaces
	var b strings.Builder
	for _, line := range lines[start:end] {
		if len(line) >= 4 && line[:4] == "    " {
			b.WriteString(line[4:])
		} else {
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// applyDocEdits rewrites a markdown file, replacing code block contents.
func applyDocEdits(t *testing.T, path string, edits []edit) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s: %v", path, err)
		return
	}

	lines := strings.Split(string(data), "\n")
	// Build a map: startLine → formatted source
	editMap := map[int]string{}
	for _, e := range edits {
		editMap[e.startLine] = e.formatted
	}

	var out strings.Builder
	inBlock := false
	blockStart := 0
	for i, line := range lines {
		lineNum := i + 1 // 1-based
		if strings.TrimSpace(line) == "```sngl" {
			inBlock = true
			blockStart = lineNum + 1
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if inBlock && strings.TrimSpace(line) == "```" {
			if replacement, ok := editMap[blockStart]; ok {
				out.WriteString(replacement)
				t.Logf("formatted %s block at line %d", path, blockStart)
			}
			inBlock = false
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if inBlock {
			if _, ok := editMap[blockStart]; ok {
				continue // skip original lines — will be replaced
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}

	os.WriteFile(path, []byte(out.String()), 0o644)
}

type edit struct {
	startLine int
	source    string
	formatted string
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
	// Skip blank lines before the fence
	i := fenceLine - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	if i < 0 {
		return "", ""
	}

	// First check: is the previous non-blank line a single-line annotation?
	prev := strings.TrimSpace(lines[i])
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
	for i = i - 1; i >= 0; i-- {
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
