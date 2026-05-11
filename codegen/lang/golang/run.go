package golang

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// RunDir implements codegen.LangRunner. It bootstraps a Go module in dir and
// runs the generated code with "go run .".
func (t *Translator) RunDir(dir, goVersion, goModExtra string, args []string) error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH")
	}

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
	if err := os.WriteFile(dir+"/go.mod", []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}

	slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stderr
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}

	runArgs := append([]string{"run", "."}, args...)
	slog.Info("exec", "cmd", "go run .", "dir", dir)
	run := exec.Command(goPath, runArgs...)
	run.Dir = dir
	run.Stdin = os.Stdin
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}
