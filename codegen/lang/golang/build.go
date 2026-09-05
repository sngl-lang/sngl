package golang

import (
	"context"
	"fmt"
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
		if out, err := codegen.TidyModule(context.Background(), dir); err != nil {
			fmt.Fprint(os.Stderr, out)
			return "", fmt.Errorf("go mod tidy: %w", err)
		}
	}

	artifact := filepath.Join(dir, "app")
	out, err := goBuild(context.Background(), goPath, dir, artifact)
	if err != nil {
		fmt.Fprint(os.Stderr, out)
		return "", fmt.Errorf("go build: %w", err)
	}
	return artifact, nil
}
