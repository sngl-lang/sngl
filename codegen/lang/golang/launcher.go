package golang

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
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

	needTidy, err := codegen.WriteGoMod(dir, "", "")
	if err != nil {
		return nil, nil, err
	}
	if needTidy {
		if out, err := codegen.TidyModule(ctx, dir); err != nil {
			if reason, ok := detectMissingPlatformLib(out); ok {
				return nil, nil, &codegen.SkipError{Reason: reason}
			}
			fmt.Fprint(os.Stderr, out)
			return nil, nil, fmt.Errorf("go mod tidy: %w", err)
		}
	}

	// Build the agent alongside dir rather than inside it. `go build -o`
	// re-links from scratch whenever its output file is missing, and dir
	// is emptied before each generation (see codegen.BuildDir), so an
	// in-dir binary would be relinked on every run. A sibling path
	// survives the wipe, letting go see an up-to-date binary and skip
	// the link — about 0.4s per `sngl test` invocation.
	binPath := dir + ".testagent"
	out, buildErr := goBuild(ctx, goPath, dir, binPath)
	if buildErr != nil {
		if reason, ok := detectMissingPlatformLib(out); ok {
			return nil, nil, &codegen.SkipError{Reason: reason}
		}
		fmt.Fprint(os.Stderr, out)
		return nil, nil, fmt.Errorf("go build: %w", buildErr)
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

// detectMissingPlatformLib looks for known pkg-config "package not
// found" signatures in build output. Returns a human-readable reason
// + true when one is found.
func detectMissingPlatformLib(buildOut string) (string, bool) {
	// pkg-config writes lines like:
	//   Package gtk4 was not found in the pkg-config search path.
	// followed by "No package 'gtk4' found".
	cases := []struct {
		needle string
		reason string
	}{
		{"Package gtk4 was not found", "gtk4 dev libraries not installed (pkg-config)"},
		{"No package 'gtk4' found", "gtk4 dev libraries not installed (pkg-config)"},
		{"Package gtk+-3.0 was not found", "gtk3 dev libraries not installed (pkg-config)"},
	}
	for _, c := range cases {
		if strings.Contains(buildOut, c.needle) {
			return c.reason, true
		}
	}
	return "", false
}
