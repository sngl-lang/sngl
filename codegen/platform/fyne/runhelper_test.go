package fyne

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "duckfam.us/sngl/codegen/lang"
)

// generateForFyne is the emitted program, as a build makes it.
func generateForFyne(t *testing.T, src string) []byte {
	t.Helper()
	return []byte(generateFyneModelBuilt(t, src))
}

// runEmitted writes model.go and driver into a temp dir and runs `go test`
// there. The dir is under the main module so the fyne imports resolve through
// the project's own go.mod.
func runEmitted(t *testing.T, prefix string, modelSrc []byte, driver string) {
	t.Helper()
	tmp, err := os.MkdirTemp(".", "_"+prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = tmp
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Errorf("running the emitted program failed: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			runErr, combined, modelSrc)
	}
}
