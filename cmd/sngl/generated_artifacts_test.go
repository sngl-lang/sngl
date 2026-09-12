package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Artifacts that regenerate differently from what is committed, each named
// with the issue that explains why. An entry tolerates a *trailing whitespace*
// difference and nothing else, so the exemption cannot quietly widen into
// cover for a real drift -- and the test reports one that has stopped being
// needed rather than leaving it to rot.
var knownArtifactDrift = map[string]string{
	// #203: the html emitter writes a trailing blank line that gofmt then
	// strips, so every `go generate` dirties the file. Invisible in a verify
	// run because verify's own `go fmt ./...` step undoes it a moment later.
	"internal/docbrowser/server.go": "#203: regenerates with a trailing blank line that gofmt strips",
}

// A committed generated file that no longer matches its source reads as
// correct: it compiles, the tests pass, and nothing regenerates it. #196
// renamed a symbol inside examples/todo/ui/model.go and the whole suite stayed
// green.
//
// Discovery is a walk rather than a list, so a `go:generate` line added
// anywhere is covered without editing this test.
//
// It lives here rather than in a txtar script because a script runs in a
// sandbox holding its own copy of the source: it can assert what the CLI
// generates, but never that the *committed* file still matches. The CLI is the
// real one either way -- this runs it in-process, as snglCmd does.
func TestCommittedGeneratedArtifactsMatchTheirSource(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dirs := findSNGLGenerateDirectives(t, root)
	if len(dirs) == 0 {
		t.Fatal("no `go:generate go tool sngl generate` directives found: the walk is looking in the wrong place")
	}

	seen := map[string]bool{}
	for _, d := range dirs {
		t.Run(d.label(root), func(t *testing.T) {
			for name, got := range regenerate(t, d) {
				committed := filepath.Join(d.outDir(), name)
				rel, _ := filepath.Rel(root, committed)
				seen[filepath.ToSlash(rel)] = true
				compareArtifact(t, committed, filepath.ToSlash(rel), got)
			}
		})
	}

	for name := range knownArtifactDrift {
		if !seen[name] {
			t.Errorf("%s is exempted but no directive generates it: drop the entry", name)
		}
	}
}

func compareArtifact(t *testing.T, path, rel string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s: generated but not committed: %v", rel, err)
		return
	}
	reason, exempt := knownArtifactDrift[rel]
	switch {
	case bytes.Equal(got, want):
		if exempt {
			t.Errorf("%s: matches its source again -- remove the exemption (%s)", rel, reason)
		}
	case exempt && bytes.Equal(bytes.TrimRight(got, "\n"), bytes.TrimRight(want, "\n")):
		t.Logf("%s: known trailing-whitespace drift (%s)", rel, reason)
	default:
		t.Errorf("%s is stale: it no longer matches what its source generates.\n%s",
			rel, clipLines(lineDiff(string(want), string(got))))
	}
}

// An html page is emitted as one Go string literal, so the differing "line"
// can be the whole document. The identity of the line is what a reader needs.
func clipLines(diff string) string {
	var out []string
	for _, line := range strings.Split(diff, "\n") {
		if len(line) > 160 {
			line = line[:160] + "… (clipped)"
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// directive is one `//go:generate go tool sngl generate ...` line: the
// arguments as written, and the directory they are read in.
type directive struct {
	dir  string
	file string
	line int
	args []string
}

func (d directive) label(root string) string {
	rel, _ := filepath.Rel(root, d.file)
	return strings.ReplaceAll(rel, string(filepath.Separator), "_")
}

// outDir is where the committed artifacts of this directive live. `sngl
// generate` writes into the working directory unless --out says otherwise.
func (d directive) outDir() string {
	for i, a := range d.args {
		switch {
		case a == "--out" || a == "-o":
			if i+1 < len(d.args) {
				return filepath.Join(d.dir, d.args[i+1])
			}
		case strings.HasPrefix(a, "--out="):
			return filepath.Join(d.dir, strings.TrimPrefix(a, "--out="))
		}
	}
	return d.dir
}

// argsWithoutOut is the directive minus its --out, so the caller can redirect
// the write without the original winning or being relied on to lose.
func (d directive) argsWithoutOut() []string {
	var out []string
	for i := 0; i < len(d.args); i++ {
		a := d.args[i]
		if a == "--out" || a == "-o" {
			i++
			continue
		}
		if strings.HasPrefix(a, "--out=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func findSNGLGenerateDirectives(t *testing.T, root string) []directive {
	t.Helper()
	var found []directive
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			switch e.Name() {
			case ".git", "_site", "node_modules", "tmp", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "//go:generate go tool sngl generate")
			if !ok {
				continue
			}
			found = append(found, directive{
				dir:  filepath.Dir(path),
				file: path,
				line: i + 1,
				args: append([]string{"generate"}, strings.Fields(rest)...),
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// regenerate runs the directive against a scratch output directory and returns
// what it wrote, keyed by path relative to that directory.
func regenerate(t *testing.T, d directive) map[string][]byte {
	t.Helper()
	out := t.TempDir()

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(d.dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	args := append(d.argsWithoutOut(), "--out", out, "-q")
	resetFlags(rootCmd)
	rootCmd.SetArgs(args)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("%s:%d: %v", d.file, d.line, err)
	}

	files := map[string][]byte{}
	err = filepath.WalkDir(out, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(out, path)
		if err != nil {
			return err
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("%s:%d: generated nothing", d.file, d.line)
	}
	return files
}
