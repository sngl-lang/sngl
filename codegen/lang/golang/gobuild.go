package golang

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// goBuild runs `go build -trimpath -o out .` in dir and returns its combined
// output.
//
// A failure that names the module graph rather than the code is retried once
// after `go mod tidy`. codegen.WriteGoMod seeds the temp module from the
// host's requires and go.sum, which covers the generated code in the ordinary
// case and saves a tidy on every invocation; it cannot cover a package whose
// own dependencies the host module never pulls in. See codegen.NeedsModuleTidy.
func goBuild(ctx context.Context, goPath, dir, out string) (string, error) {
	run := func() (string, error) {
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, goPath, "build", "-trimpath", "-o", out, ".")
		cmd.Dir = dir
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	}

	slog.Info("exec", "cmd", "go build", "dir", dir, "out", out)
	output, err := run()
	if err == nil || !codegen.NeedsModuleTidy(output) {
		return output, err
	}
	if tidyOut, tidyErr := codegen.TidyModule(ctx, dir); tidyErr != nil {
		return tidyOut, tidyErr
	}
	return run()
}
