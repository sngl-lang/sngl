package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Pins the committed generated regions to the grammar: nothing else notices
// when sngl.ebnf changes and specgen is not re-run. mdox runs too, because it
// reflows what the generator writes.
func TestSpecificationIsGenerated(t *testing.T) {
	root := repoRoot()
	spec := filepath.Join(root, "docs", "reference", "specification.md")
	want, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	// Not t.TempDir(): mdox refuses a file outside its anchor (the repo root),
	// and not under docs/ either — TestDocSNGLBlocks enumerates that tree and
	// raced with the transient file.
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
