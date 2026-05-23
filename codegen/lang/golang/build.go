package golang

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Build implements codegen.Builder for any Go-emitting platform that doesn't
// provide its own. Bootstraps a go.mod in dir, runs `go mod tidy`, then
// `go build -o app .`. Returns the absolute path of the produced binary;
// the caller (sngl build) moves/renames it.
func (t *Translator) Build(dir string, opts *ir.StructLit) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go not found in PATH")
	}

	goVersion, goModExtra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	goMod := fmt.Sprintf("module tmp\n\ngo %s\n", goVersion)
	if goModExtra != "" {
		goMod += "\n" + goModExtra
		if !strings.HasSuffix(goMod, "\n") {
			goMod += "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return "", fmt.Errorf("writing go.mod: %w", err)
	}

	slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stderr
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return "", fmt.Errorf("go mod tidy: %w", err)
	}

	artifact := filepath.Join(dir, "app")
	slog.Info("exec", "cmd", "go build -o app .", "dir", dir)
	bld := exec.Command(goPath, "build", "-o", artifact, ".")
	bld.Dir = dir
	bld.Stdout = os.Stderr
	bld.Stderr = os.Stderr
	if err := bld.Run(); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}
	return artifact, nil
}
