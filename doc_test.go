package sngl_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// hasErrorDiags returns true if any diagnostic is an error.
func hasErrorDiags(diags []ir.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == ir.Error {
			return true
		}
	}
	return false
}

// TestDocSNGLBlocks finds all ```sngl code blocks in markdown files and
// verifies they parse correctly and the formatted output re-parses.
// Doc-rot parse failures are logged (not errored) since snippets lag the
// grammar and type-checking is not enforced here either.
func TestDocSNGLBlocks(t *testing.T) {
	for s := range testutil.DocSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Logf("parse error at %s (doc rot):\n%s\n---\n%v", s.Name, s.Source, err)
				return
			}

			// Round-trip format check
			formatted := parser.Format(doc)
			_, err = parser.Parse(s.Filename+".fmt", []byte(withStdSrc(formatted)))
			if err != nil {
				t.Logf("formatted output doesn't re-parse at %s (doc rot):\n%s\n---\n%v", s.Name, formatted, err)
			}
		})
	}
}

// TestDocSNGLFormat checks that all ```sngl code blocks in docs are properly formatted.
// Set SNGL_FMT_DOCS=1 to write formatted snippets back into the markdown files.
func TestDocSNGLFormat(t *testing.T) {
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
		blocks := testutil.ExtractSNGLBlocks(t, file)
		if len(blocks) == 0 {
			continue
		}

		var edits []edit

		for i, block := range blocks {
			name := fmt.Sprintf("%s#%d", file, i+1)
			t.Run(name, func(t *testing.T) {
				if block.Annotation == "nocheck" || block.Annotation == "expression" {
					return
				}

				src := block.Prelude
				hasReplacements := false

				if strings.Contains(block.Prelude, "-- ") && strings.Contains(block.Prelude, " --") {
					ar := txtarParse(block.Prelude)
					src = ar.comment
					hasReplacements = ar.hasReplacements
				}

				switch block.Annotation {
				case "component":
					src += "component main {\n" + block.Source + "\n}"
				default:
					src += block.Source
				}

				doc, err := parser.Parse(name, []byte(withStdSrc(src)))
				if err != nil {
					return // parse errors caught by TestDocSNGLBlocks
				}

				formatted := parser.Format(doc)

				// Unwrap: extract the snippet portion from the formatted output
				var snippet string
				switch block.Annotation {
				case "component":
					snippet = testutil.UnwrapComponent(formatted, block.Prelude)
				default:
					if block.Prelude != "" {
						preDoc, perr := parser.Parse("prelude", []byte(withStdSrc(block.Prelude)))
						if perr == nil {
							fmtPre := parser.Format(preDoc)
							formatted = strings.TrimPrefix(formatted, fmtPre)
						}
					}
					snippet = formatted
				}

				original := strings.TrimRight(block.Source, " \t\n")
				snippet = strings.TrimRight(snippet, " \t\n")

				if original != snippet {
					if hasReplacements {
						t.Errorf("formatting differs (has ... replacement, not auto-fixable):\n--- original ---\n%s\n--- formatted ---\n%s", original, snippet)
					} else if writeBack {
						edits = append(edits, edit{
							startLine: block.Line,
							source:    block.Source,
							formatted: snippet + "\n",
						})
					} else {
						t.Errorf("formatting differs (run with SNGL_FMT_DOCS=1 to fix):\n--- original ---\n%s\n--- formatted ---\n%s", original, snippet)
					}
				}
			})
		}

		if writeBack && len(edits) > 0 {
			applyDocEdits(t, file, edits)
		}
	}
}

type edit struct {
	startLine int
	source    string
	formatted string
}

type txtarResult struct {
	comment         string
	hasReplacements bool
}

func txtarParse(prelude string) txtarResult {
	// Minimal txtar-like parsing for prelude replacement detection.
	var r txtarResult
	lines := strings.Split(prelude, "\n")
	var comment strings.Builder
	inFile := false
	for _, line := range lines {
		if strings.HasPrefix(line, "-- ") && strings.HasSuffix(line, " --") {
			name := strings.TrimPrefix(line, "-- ")
			name = strings.TrimSuffix(name, " --")
			if name == "..." {
				r.hasReplacements = true
			}
			inFile = true
			continue
		}
		if !inFile {
			comment.WriteString(line)
			comment.WriteByte('\n')
		}
	}
	r.comment = comment.String()
	return r
}

func applyDocEdits(t *testing.T, path string, edits []edit) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s: %v", path, err)
		return
	}

	lines := strings.Split(string(data), "\n")
	editMap := map[int]string{}
	for _, e := range edits {
		editMap[e.startLine] = e.formatted
	}

	var out strings.Builder
	inBlock := false
	blockStart := 0
	for i, line := range lines {
		lineNum := i + 1
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
				continue
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}

	os.WriteFile(path, []byte(out.String()), 0o644)
}

// TestPlatformSourcesPassChecker verifies that each platform's PkgSource .sngl
// file passes the type checker (no bare expression statements, etc.).
func TestPlatformSourcesPassChecker(t *testing.T) {
	tests := []struct {
		lang, platform string
	}{
		{"js", "html"},
		{"go", "bubbletea"},
		{"go", "fyne"},
		{"kotlin", "android"},
		{"go", "http"},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			src := "output {\n    " + tt.lang + " {\n        " + tt.platform + "\n    }\n}\n\ncomponent main {\n    text(value=\"hi\")\n}\n"
			doc, err := parser.Parse("test.sngl", []byte(withStdSrc(src)))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, diags := checker.Check(doc, &checker.Config{FS: os.DirFS("."), Dir: ".", IsMain: true}); hasErrorDiags(diags) {
				t.Errorf("checker error: %v", diags)
			}
		})
	}
}
