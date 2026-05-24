package golang

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LaunchTest implements codegen.TestLauncher for the default Go path:
// synth go.mod, go mod tidy, go build, exec the binary with stdin/
// stdout connected for JSON-RPC. Platforms whose lifecycle differs
// (e.g. windowed GUI threads, mobile install/launch) implement their
// own LaunchTest and override this fallback.
func (t *Translator) LaunchTest(ctx context.Context, dir string, _ codegen.LangTranslator, _ *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, nil, fmt.Errorf("go not found in PATH")
	}

	if err := writeTestGoMod(dir); err != nil {
		return nil, nil, err
	}

	slog.Info("exec", "cmd", "go mod tidy", "dir", dir)
	tidy := exec.CommandContext(ctx, goPath, "mod", "tidy")
	tidy.Dir = dir
	tidy.Stdout = os.Stderr
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return nil, nil, fmt.Errorf("go mod tidy: %w", err)
	}

	binPath := filepath.Join(dir, "testagent_bin")
	slog.Info("exec", "cmd", "go build", "dir", dir, "out", binPath)
	bld := exec.CommandContext(ctx, goPath, "build", "-o", binPath, ".")
	bld.Dir = dir
	bld.Stdout = os.Stderr
	bld.Stderr = os.Stderr
	if err := bld.Run(); err != nil {
		return nil, nil, fmt.Errorf("go build: %w", err)
	}

	cmd := exec.CommandContext(ctx, binPath)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}

	ch := &pipeChannel{in: stdout, out: stdin, cmd: cmd}
	cleanup := func() {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return ch, cleanup, nil
}

// writeTestGoMod synthesises go.mod for the temp test build dir. Mirrors
// the existing build.go path so behaviour is consistent.
func writeTestGoMod(dir string) error {
	goVersion, goModExtra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	mod := fmt.Sprintf("module tmp\n\ngo %s\n", goVersion)
	if goModExtra != "" {
		mod += "\n" + goModExtra
		if !strings.HasSuffix(mod, "\n") {
			mod += "\n"
		}
	}
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
}

// pipeChannel adapts an exec.Cmd's stdout/stdin to RPCChannel.
type pipeChannel struct {
	in  io.ReadCloser
	out io.WriteCloser
	cmd *exec.Cmd
}

func (p *pipeChannel) Read(b []byte) (int, error)  { return p.in.Read(b) }
func (p *pipeChannel) Write(b []byte) (int, error) { return p.out.Write(b) }
func (p *pipeChannel) Close() error {
	_ = p.out.Close()
	return p.in.Close()
}
