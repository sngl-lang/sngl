package golang

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
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
	env, err := crossEnv(dir)
	if err != nil {
		return "", err
	}
	run := func() (string, error) {
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, goPath, "build", "-o", out, ".")
		cmd.Dir = dir
		if env != nil {
			cmd.Env = append(os.Environ(), env...)
		}
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

// crossEnv is the environment a program dir holds is built in for the
// target its manifest names (clang.BuildFile, which win32 writes for Go as
// for C), or nil for the host: cgo, with the C compiler that targets it.
func crossEnv(dir string) ([]string, error) {
	target := clang.TargetOf(dir)
	if target == "" {
		return nil, nil
	}
	goos, goarch, ok := strings.Cut(target, "/")
	if !ok {
		return nil, fmt.Errorf("%s: a target is os/arch, not %q", clang.BuildFile, target)
	}
	cc, err := clang.ForTarget(target, "")
	if err != nil {
		return nil, err
	}
	return []string{"GOOS=" + goos, "GOARCH=" + goarch, "CGO_ENABLED=1", "CC=" + cc.Path}, nil
}

// exeFor is the file a build of dir into out writes: out.exe where it is
// built for Windows, which names nothing else a program.
func exeFor(dir, out string) string {
	if strings.HasPrefix(clang.TargetOf(dir), "windows/") && !strings.HasSuffix(out, ".exe") {
		return out + ".exe"
	}
	return out
}
