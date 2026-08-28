package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSpecificationIsGenerated pins the committed generated regions to the
// grammar: nothing else notices when sngl.ebnf changes and specgen is not
// re-run. The whole pipeline runs over a copy, mdox included, because mdox
// reflows what the generator writes.
//
// Where that copy lives is constrained from both sides — see below.
func TestSpecificationIsGenerated(t *testing.T) {
	root := repoRoot()
	spec := filepath.Join(root, "docs", "reference", "specification.md")
	want, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	// Not t.TempDir(): mdox is anchored on the repo root (generate runs it
	// with cmd.Dir = root) and refuses a file outside its anchor, so the copy
	// has to live under root. It must not live under docs/ either — that tree
	// is a corpus another package's test enumerates and then reads, and a
	// transient .md in it made TestDocSNGLBlocks fail on a file that had
	// already been cleaned up. A scratch directory at the root satisfies both.
	dir, err := os.MkdirTemp(root, ".specgen-check-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	tmp := filepath.Join(dir, "specification.md")
	if err := os.WriteFile(tmp, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, tmp); err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("docs/reference/specification.md is stale; run `go run ./internal/cmd/specgen`")
	}
}
