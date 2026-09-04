package bubbletea

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

// buildGeneratedGo type-checks generated Go by compiling it.
//
// The temp dir is created UNDER this package so charm.land/bubbletea and the
// sngl runtime packages resolve through the repo's own go.mod; a dir in
// os.TempDir() has no module above it.
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
	cmd := exec.Command("go", "build", "-gcflags=-e", ".")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("generated Go does not compile: %v\n--- go build ---\n%s\n--- model.go ---\n%s", err, out, model)
	}
}
