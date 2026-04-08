package fyne

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

// Run implements codegen.Runner. It bootstraps a Go module in the output
// directory and runs the generated code with "go run .".
func (g *Generator) Run(dir string, _ map[string]string, args []string) error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH")
	}

	// Bootstrap go.mod
	if err := os.WriteFile(dir+"/go.mod", []byte("module tmp\n\ngo 1.23\n"), 0o644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}

	// Download dependencies
	slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stderr
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return fmt.Errorf("go mod tidy: %w", err)
	}

	// Run the program
	runArgs := append([]string{"run", "."}, args...)
	slog.Info("exec", "cmd", "go run .", "dir", dir)
	run := exec.Command(goPath, runArgs...)
	run.Dir = dir
	run.Stdin = os.Stdin
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}
