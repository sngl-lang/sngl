package sngl_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
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

// TestDocSNGLBlocks type-checks every ```sngl block in the docs, and checks
// that the formatted output re-parses.
//
// These used to log rot rather than fail, which meant a snippet could stop
// compiling and nothing said so — the docs accumulated an ambient `event`
// identifier, string-valued enums, a lambda form and a `style` declaration
// that the language does not have. A block that cannot be checked as written
// carries an annotation saying so: SNGL-component and SNGL-expression wrap a
// fragment, an annotation may carry a prelude of supporting declarations, and
// SNGL-nocheck opts out entirely (for signatures and other non-programs).
func TestDocSNGLBlocks(t *testing.T) {
	langs, plats := testtargets.Targets()
	for s := range testutil.DocSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse error:\n%s\n---\n%v", s.Source, err)
			}

			_, diags := checker.Check(doc, &checker.Config{
				IsMain: true, FS: s.FS, Dir: s.Dir,
				Languages: langs, Platforms: plats,
			})
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Errorf("%s: %s", d.Pos, d.Msg)
				}
			}
			if t.Failed() {
				t.Logf("source:\n%s", s.Source)
			}

			// Round-trip format check
			formatted := parser.Format(doc)
			if _, err := parser.Parse(s.Filename+".fmt", []byte(withStdSrc(formatted))); err != nil {
				t.Errorf("formatted output doesn't re-parse:\n%s\n---\n%v", formatted, err)
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

// TestPlatformSourcesPassChecker verifies that each platform's
// lib/platforms/<name>/ source passes the type checker (no bare expression
// statements, etc.).
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

// TestFrontPageExamplesCheck type-checks the two front-page programs. Unlike
// the doc-block tests above, which log rot rather than fail because snippets
// are fragments that lag the grammar, these are complete programs and the
// first SNGL most readers see. Both stayed broken through a migration and
// three syntax changes: nothing walked README.md at all, and the site's copy
// was only ever parsed, never checked.
func TestFrontPageExamplesCheck(t *testing.T) {
	for _, file := range []string{"README.md", "docs/index.md"} {
		t.Run(file, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			const fence = "```sngl\n"
			i := strings.Index(string(src), fence)
			if i < 0 {
				t.Fatalf("%s has no ```sngl block", file)
			}
			rest := string(src)[i+len(fence):]
			program, _, ok := strings.Cut(rest, "\n```")
			if !ok {
				t.Fatalf("%s sngl block is unterminated", file)
			}

			doc, err := parser.Parse(file, []byte(program))
			if err != nil {
				t.Fatalf("parse: %v\n%s", err, program)
			}
			_, diags := checker.Check(doc, &checker.Config{IsMain: true})
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Errorf("%s: %s", d.Pos, d.Msg)
				}
			}
		})
	}
}

// TestGettingStartedSnippetsCheck type-checks the SNGL snippets embedded in
// the Getting Started page. The page is a .sngl program whose prose and code
// are `docui.CodeBlock` arguments, so the markdown harness never saw it and
// nothing checked the code it teaches: every snippet was missing the stdlib
// import, and it taught `list.push(xs, v)`, `@effect(...)` and `--main=false`,
// none of which exist.
//
// A snippet is skipped only when it says so itself: `// ...` marks elided
// code, and a non-sngl scheme import names a package the reader supplies.
func TestGettingStartedSnippetsCheck(t *testing.T) {
	const page = "internal/learn/getting_started.sngl"
	src, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("(?s)docui\\.CodeBlock\\(code=`(.*?)`,\\s*language=\"sngl\"\\)")
	matches := re.FindAllStringSubmatch(string(src), -1)
	if len(matches) == 0 {
		t.Fatalf("%s: no sngl CodeBlocks found — did the call shape change?", page)
	}
	langs, plats := testtargets.Targets()
	var checked int
	for i, m := range matches {
		snippet := m[1]
		if strings.Contains(snippet, "// ...") || importsForeignScheme(snippet) {
			continue
		}
		checked++
		t.Run(fmt.Sprintf("snippet%d", i+1), func(t *testing.T) {
			doc, err := parser.Parse(page, []byte(snippet))
			if err != nil {
				t.Fatalf("parse: %v\n%s", err, snippet)
			}
			_, diags := checker.Check(doc, &checker.Config{
				IsMain: true, Languages: langs, Platforms: plats,
			})
			for _, d := range diags {
				if d.Severity == ir.Error {
					t.Errorf("%s: %s\n%s", d.Pos, d.Msg, snippet)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("every snippet was skipped; the skip conditions are too broad")
	}
}

// importsForeignScheme reports whether src imports under a scheme other than
// sngl://, which means it names a package the reader supplies.
func importsForeignScheme(src string) bool {
	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "import ") {
			continue
		}
		if strings.Contains(line, "://") && !strings.Contains(line, "sngl://") {
			return true
		}
	}
	return false
}
