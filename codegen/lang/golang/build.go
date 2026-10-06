package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterCommand("go.buildModule", func(_ *ir.StructLit, args []any) (any, error) {
		str := func(i int) string {
			if i < len(args) {
				s, _ := args[i].(string)
				return s
			}
			return ""
		}
		return buildModule(str(0), str(1), str(2), str(3))
	})
}

// buildModule answers `go.buildModule`, which sngl:language/go's run and build
// commands call: it bootstraps a go.mod in dir, tidies it when it is new, and
// builds the program into out.
func buildModule(dir, out, goVersion, goModExtra string) (string, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go not found in PATH")
	}
	needTidy, err := codegen.WriteGoMod(dir, goVersion, goModExtra)
	if err != nil {
		return "", fmt.Errorf("writing go.mod: %w", err)
	}
	if needTidy {
		if output, err := codegen.TidyModule(context.Background(), dir); err != nil {
			fmt.Fprint(os.Stderr, output)
			return "", fmt.Errorf("go mod tidy: %w", err)
		}
	}
	// Build, then exec -- rather than `go run`, which interleaves the go
	// tool's own diagnostics with the program's output. goBuild has to read
	// its output to tell a module-graph failure from a compile error, and it
	// can only do that while nothing else is writing to the same stream.
	if output, err := goBuild(context.Background(), goPath, dir, out); err != nil {
		fmt.Fprint(os.Stderr, output)
		return "", fmt.Errorf("go build: %w", err)
	}
	return out, nil
}
