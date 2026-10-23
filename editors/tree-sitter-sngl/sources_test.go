package treesitter_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The corpus is the grammar's own account of SNGL, and it went on passing
// while the language moved on underneath it: by the time anyone looked, the
// grammar refused 761 of the repository's 817 files and every editor that
// used it highlighted nothing. These tests hold it to the SNGL the compiler
// reads.

var sourceRoots = []string{"testdata", "examples", "lib"}

func treeSitter(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("tree-sitter")
	if err != nil {
		t.Skip("tree-sitter binary not on PATH")
	}
	return bin
}

// sources is every .sngl file the compiler is expected to parse, less the
// fixtures that exist to be refused by its parser and the snapshot goldens,
// which record a rendered tree rather than a program.
func sources(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, root := range sourceRoots {
		err := filepath.WalkDir(filepath.Join("..", "..", root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && strings.HasSuffix(path, ".snapshots") {
				return filepath.SkipDir
			}
			if d.IsDir() || filepath.Ext(path) != ".sngl" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Contains(src, []byte("ERROR(parse")) {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func TestGrammarParsesSources(t *testing.T) {
	bin := treeSitter(t)
	paths := sources(t)
	list := filepath.Join(t.TempDir(), "paths")
	if err := os.WriteFile(list, []byte(strings.Join(paths, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	// parse exits non-zero when any file has an error, and -q leaves a line
	// for each such file naming its first one.
	out, _ := exec.Command(bin, "parse", "-q", "--paths", list).CombinedOutput()
	var failed []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(line, "(ERROR") || strings.Contains(line, "(MISSING") {
			failed = append(failed, strings.Join(strings.Fields(line), " "))
		}
	}
	if len(failed) > 0 {
		t.Errorf("the grammar refuses %d of %d files:\n%s", len(failed), len(paths), strings.Join(failed, "\n"))
	}
}

func TestQueriesCompile(t *testing.T) {
	bin := treeSitter(t)
	queries, err := filepath.Glob("queries/*.scm")
	if err != nil || len(queries) == 0 {
		t.Fatalf("no queries: %v", err)
	}
	sample := filepath.Join("..", "..", "examples", "pokedex", "app.sngl")
	for _, q := range queries {
		if out, err := exec.Command(bin, "query", "-q", q, sample).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", q, err, out)
		}
	}
}

// TestNeovimQueriesAreTheGrammars holds the plugin to the grammar's queries:
// a copy of its own drifted, named node types the grammar had dropped, and
// Neovim refused the whole file.
func TestNeovimQueriesAreTheGrammars(t *testing.T) {
	dir := filepath.Join("..", "neovim", "queries", "sngl")
	target, err := os.Readlink(dir)
	if err != nil {
		t.Fatalf("%s should be a link to the grammar's queries: %v", dir, err)
	}
	if want := filepath.Join("..", "..", "tree-sitter-sngl", "queries"); target != want {
		t.Errorf("%s links to %s, want %s", dir, target, want)
	}
}

// TestParserIsGenerated holds src/ to grammar.js: an edit to the grammar
// that is not regenerated reaches no editor.
func TestParserIsGenerated(t *testing.T) {
	bin := treeSitter(t)
	out := t.TempDir()
	if msg, err := exec.Command(bin, "generate", "-o", out).CombinedOutput(); err != nil {
		t.Skipf("tree-sitter generate: %v\n%s", err, msg)
	}
	for _, f := range []string{"grammar.json", "node-types.json"} {
		got, err := os.ReadFile(filepath.Join("src", f))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(out, f))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("src/%s is not what grammar.js generates: run tree-sitter generate", f)
		}
	}
}
