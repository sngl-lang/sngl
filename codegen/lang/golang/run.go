package golang

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// RunDir implements codegen.LangRunner. It bootstraps a Go module in dir and
// runs the generated code with "go run .".
func (t *Translator) RunDir(dir, goVersion, goModExtra string, args []string) error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH")
	}

	needTidy, err := codegen.WriteGoMod(dir, goVersion, goModExtra)
	if err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	if needTidy {
		slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
		tidy := exec.Command(goPath, "mod", "tidy")
		tidy.Dir = dir
		tidy.Stdout = os.Stderr
		tidy.Stderr = os.Stderr
		if err := tidy.Run(); err != nil {
			return fmt.Errorf("go mod tidy: %w", err)
		}
	}

	runArgs := append([]string{"run", "-trimpath", "."}, args...)
	slog.Info("exec", "cmd", "go run .", "dir", dir)
	run := exec.Command(goPath, runArgs...)
	run.Dir = dir
	run.Stdin = os.Stdin
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}
