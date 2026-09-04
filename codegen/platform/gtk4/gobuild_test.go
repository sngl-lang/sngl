package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixtureSource reads a root testdata fixture. A defect that only stock
// fixtures show is one a hand-written program in this file would not have.
func fixtureSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// buildGeneratedGo type-checks generated Go by compiling it. gtk4 builds rather
// than runs: constructing a widget needs a display, and what these tests are
// about is the types.
//
// The temp dir is created UNDER this package so gtk4rt resolves through the
// repo's own go.mod; a dir in os.TempDir() has no module above it. The
// emission is `package main` without a main(), which links only under `sngl
// run`'s own scaffold, so one is supplied here.
func buildGeneratedGo(t *testing.T, prefix, model string) {
	t.Helper()
	tmp, err := os.MkdirTemp(".", prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "entry.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-gcflags=-e", ".")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("generated Go does not compile: %v\n--- go build ---\n%s\n--- model.go ---\n%s", err, out, model)
	}
}
