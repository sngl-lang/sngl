package golang

import (
	"context"
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
		if out, err := codegen.TidyModule(context.Background(), dir); err != nil {
			fmt.Fprint(os.Stderr, out)
			return fmt.Errorf("go mod tidy: %w", err)
		}
	}

	// Build, then exec — rather than `go run`, which interleaves the go
	// tool's own diagnostics with the program's output. goBuild has to read
	// its output to tell a module-graph failure from a compile error, and it
	// can only do that while nothing else is writing to the same stream.
	// The binary also lands beside dir, which survives the wipe before each
	// generation, so a re-run of an unchanged program skips the link.
	binPath := dir + ".app"
	if out, err := goBuild(context.Background(), goPath, dir, binPath); err != nil {
		fmt.Fprint(os.Stderr, out)
		return fmt.Errorf("go build: %w", err)
	}

	slog.Info("exec", "cmd", binPath, "dir", dir)
	run := exec.Command(binPath, args...)
	run.Dir = dir
	run.Stdin = os.Stdin
	run.Stdout = os.Stdout
	run.Stderr = os.Stderr
	return run.Run()
}
