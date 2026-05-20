package treesitter_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTreesitterCorpus shells out to `tree-sitter test` and fails if any
// fixture in test/corpus/*.txt doesn't match the grammar's actual output.
// Skipped when the tree-sitter binary isn't available (e.g. CI without the
// toolchain installed).
func TestTreesitterCorpus(t *testing.T) {
	if _, err := exec.LookPath("tree-sitter"); err != nil {
		t.Skip("tree-sitter binary not on PATH; skipping corpus test")
	}
	cmd := exec.Command("tree-sitter", "test")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	clean := strings.ReplaceAll(string(out), "\x1b", "\\e")
	if err != nil {
		t.Fatalf("tree-sitter test failed: %v\n%s", err, clean)
	}
	if strings.Contains(clean, "✗") {
		t.Fatalf("corpus tests reported failures:\n%s", clean)
	}
}
