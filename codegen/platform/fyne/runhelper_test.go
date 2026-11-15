package fyne

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "duckfam.us/sngl/codegen/lang"
	"duckfam.us/sngl/internal/testinputs"
)

// generateForFyne is the emitted program, as a build makes it.
func generateForFyne(t *testing.T, src string) []byte {
	t.Helper()
	return []byte(generateFyneModelBuilt(t, src))
}

// runEmitted writes model.go and driver into a temp dir and runs `go test`
// there. The dir is a module resolving as the checkout does, so the fyne
// imports resolve at the project's versions.
func runEmitted(t *testing.T, prefix string, modelSrc []byte, driver string) {
	t.Helper()
	tmp := testinputs.Module(t)

	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "driver_test.go"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "-count=1", ".")
	cmd.Dir = tmp
	testinputs.Packages(t, tmp, ".")
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Errorf("running the emitted program failed: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			runErr, combined, modelSrc)
	}
}
