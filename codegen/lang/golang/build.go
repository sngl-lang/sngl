package golang

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

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

	needTidy, err := codegen.WriteGoMod(dir, "", "")
	if err != nil {
		return "", fmt.Errorf("writing go.mod: %w", err)
	}
	if needTidy {
		slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
		tidy := exec.Command(goPath, "mod", "tidy")
		tidy.Dir = dir
		tidy.Stdout = os.Stderr
		tidy.Stderr = os.Stderr
		if err := tidy.Run(); err != nil {
			return "", fmt.Errorf("go mod tidy: %w", err)
		}
	}

	artifact := filepath.Join(dir, "app")
	slog.Info("exec", "cmd", "go build -o app .", "dir", dir)
	bld := exec.Command(goPath, "build", "-trimpath", "-o", artifact, ".")
	bld.Dir = dir
	bld.Stdout = os.Stderr
	bld.Stderr = os.Stderr
	if err := bld.Run(); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}
	return artifact, nil
}
