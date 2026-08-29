package testutil

import (
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"golang.org/x/tools/txtar"
)

const (
	OriginTestdata = "testdata"
	OriginDocs     = "docs"
)

type Sample struct {
	Name     string           // subtest name, e.g. "component_simple"
	Source   string           // complete SNGL source ready to hand to any parser
	Filename string           // parser filename arg, e.g. "component_simple.sngl"
	FS       fs.FS            // filesystem for checker imports
	Dir      string           // absolute dir path for checker
	Origin   string           // OriginTestdata or OriginDocs
	Errors   []ErrorDirective // ERROR directives (empty for doc samples)
	Folds    []FoldDirective  // FOLD directives (empty for doc samples)
	// NoFmt is set by a `// NOFMT "reason"` directive: the fixture is exempt
	// from the check that it is written the way `sngl fmt` writes it.
	NoFmt       bool
	NoFmtReason string

	writeback func(*ast.Document) // set by iterator; nil if read-only
}

func (s Sample) ExpectsError(phase string) bool {
	for _, e := range s.Errors {
		if e.Phase == phase {
			return true
		}
	}
	return false
}

func (s Sample) PhaseErrors(phase string) []ErrorDirective {
	return Filter(s.Errors, phase)
}

var projectRoot = sync.OnceValue(func() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("testutil: cannot determine source file location")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
})

func TestdataSamples(t testing.TB) iter.Seq[Sample] {
	root := projectRoot()
	testdataDir := filepath.Join(root, "testdata")
	return func(yield func(Sample) bool) {
		matches, err := filepath.Glob(filepath.Join(testdataDir, "*.sngl"))
		if err != nil {
			t.Fatalf("glob testdata: %v", err)
		}
		if len(matches) == 0 {
			t.Fatalf("no *.sngl files in %s", testdataDir)
		}
		fsys := os.DirFS(testdataDir)
		for _, path := range matches {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			base := strings.TrimSuffix(filepath.Base(path), ".sngl")
			errs, err := ParseDirectives(path)
			if err != nil {
				t.Fatalf("directives %s: %v", path, err)
			}
			folds, err := ParseFoldDirectives(path)
			if err != nil {
				t.Fatalf("fold directives %s: %v", path, err)
			}
			nofmt, nofmtReason, err := ParseNoFmt(path)
			if err != nil {
				t.Fatalf("nofmt directive %s: %v", path, err)
			}
			wbPath := path
			if !yield(Sample{
				Name:        base,
				Source:      string(src),
				Filename:    base + ".sngl",
				FS:          fsys,
				Dir:         testdataDir,
				Origin:      OriginTestdata,
				Errors:      errs,
				Folds:       folds,
				NoFmt:       nofmt,
				NoFmtReason: nofmtReason,
				writeback: func(doc *ast.Document) {
					formatted := parser.Format(doc)
					os.WriteFile(wbPath, []byte(formatted), 0o644)
				},
			}) {
				return
			}
		}
	}
}

func DocSamples(t testing.TB) iter.Seq[Sample] {
	root := projectRoot()
	docsDir := filepath.Join(root, "docs")
	return func(yield func(Sample) bool) {
		editor := &docEditor{edits: map[string][]docEdit{}}
		cleanupRegistered := false

		var mdFiles []string
		filepath.WalkDir(docsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if strings.HasSuffix(path, ".md") {
				mdFiles = append(mdFiles, path)
			}
			return nil
		})

		for _, file := range mdFiles {
			blocks := ExtractSNGLBlocks(t, file)
			for i, block := range blocks {
				if block.Annotation == "nocheck" {
					continue
				}

				src := block.Prelude
				var checkFS fs.FS = os.DirFS(root)

				var replacements []string
				if strings.Contains(block.Prelude, "-- ") && strings.Contains(block.Prelude, " --") {
					ar := txtar.Parse([]byte(block.Prelude))
					src = string(ar.Comment)
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

				// A wrapped fragment's imports have to end up at file scope.
				// A doc snippet shows its imports at the top of the block, and
				// wrapping that in `component main { ... }` would bury them
				// inside the component, where they bind nothing.
				body := block.Source
				if block.Annotation == "component" || block.Annotation == "expression" {
					var hoisted string
					hoisted, body = hoistImports(body)
					src += hoisted
				}
				src = libImports(src) + src

				switch block.Annotation {
				case "component":
					src += "component main {\n" + body + "\n}"
				case "expression":
					src += "component main {\n  computed _x = " + strings.TrimSpace(body) + "\n}"
				default:
					src += body
				}

				for _, r := range replacements {
					src = strings.Replace(src, "...", r, 1)
				}

				relPath, _ := filepath.Rel(root, file)
				name := relPath + "#" + strconv.Itoa(i+1)

				annotation := block.Annotation
				prelude := block.Prelude
				blockLine := block.Line
				blockFile := file

				if !cleanupRegistered {
					cleanupRegistered = true
					t.Cleanup(func() { editor.flush(t) })
				}

				if !yield(Sample{
					Name:     name,
					Source:   src,
					Filename: name + ".sngl",
					FS:       checkFS,
					Dir:      root,
					Origin:   OriginDocs,
					writeback: func(doc *ast.Document) {
						formatted := parser.Format(doc)
						var snippet string
						switch annotation {
						case "component":
							snippet = UnwrapComponent(formatted, prelude)
						default:
							if prelude != "" {
								preDoc, perr := parser.Parse("prelude", []byte(prelude))
								if perr == nil {
									fmtPre := parser.Format(preDoc)
									formatted = strings.TrimPrefix(formatted, fmtPre)
								}
							}
							snippet = formatted
						}
						snippet = strings.TrimRight(snippet, " \t\n") + "\n"
						editor.add(blockFile, blockLine, snippet)
					},
				}) {
					return
				}
			}
		}
	}
}

type docEditor struct {
	edits map[string][]docEdit // markdown path → pending edits
}

type docEdit struct {
	startLine int    // 1-based line of first source line in the code block
	formatted string // replacement content
}

func (e *docEditor) add(file string, startLine int, formatted string) {
	e.edits[file] = append(e.edits[file], docEdit{startLine: startLine, formatted: formatted})
}

func (e *docEditor) flush(t testing.TB) {
	t.Helper()
	for path, edits := range e.edits {
		if len(edits) == 0 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		lines := strings.Split(string(data), "\n")
		editMap := map[int]string{}
		for _, ed := range edits {
			editMap[ed.startLine] = ed.formatted
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
					continue // skip original lines
				}
			}
			out.WriteString(line)
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
		}
		os.WriteFile(path, []byte(out.String()), 0o644)
	}
}

// libImports returns the library imports a doc snippet needs but did not
// write, so a fragment about `list<T>` need not open with boilerplate. Both
// packages are dot-imported: a snippet writes `text(...)` and `circle(...)`
// unqualified. Nothing is added when the snippet imports for itself, so a
// snippet demonstrating the alias form keeps its own spelling.
func libImports(src string) string {
	if strings.Contains(src, "sngl:") {
		return ""
	}
	return "import . \"sngl:ui\"\nimport . \"sngl:app\"\nimport . \"sngl:time\"\nimport . \"sngl:dialog\"\nimport . \"sngl:macro\"\nimport . \"sngl:ui/draw\"\n"
}

// Splits the leading run of import declarations off a block body.
func hoistImports(body string) (imports, rest string) {
	lines := strings.Split(body, "\n")
	cut := 0
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if !strings.HasPrefix(t, "import ") {
			cut = i
			break
		}
		imports += t + "\n"
		cut = i + 1
	}
	return imports, strings.Join(lines[cut:], "\n")
}
