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
func TestSpecificationIsGenerated(t *testing.T) {
	root := repoRoot()
	spec := filepath.Join(root, "docs", "reference", "specification.md")
	want, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	// The copy lives beside the original: mdox refuses a file outside the
	// directory it is anchored on.
	f, err := os.CreateTemp(filepath.Dir(spec), "specgen-check-*.md")
	if err != nil {
		t.Fatal(err)
	}
	tmp := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(tmp) })
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
