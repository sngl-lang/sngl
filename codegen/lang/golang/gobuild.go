package golang

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// goBuild runs `go build -buildvcs=false -o out .` in dir and returns its
// combined output.
//
// VCS stamping is off because dir is sngl's build cache, and what encloses it
// is whatever repository the user's home happens to be -- a dotfiles checkout
// is common, and one git cannot read from there fails the build with "error
// obtaining VCS status: exit status 128". A generated program has no revision
// worth recording in any case.
//
// A failure that names the module graph rather than the code is retried once
// after `go mod tidy`. codegen.WriteGoMod seeds the temp module from the
// host's requires and go.sum, which covers the generated code in the ordinary
// case and saves a tidy on every invocation; it cannot cover a package whose
// own dependencies the host module never pulls in. See codegen.NeedsModuleTidy.
func goBuild(ctx context.Context, goPath, dir, out string) (string, error) {
	run := func() (string, error) {
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, goPath, "build", "-buildvcs=false", "-o", out, ".")
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
