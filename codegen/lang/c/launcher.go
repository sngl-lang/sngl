package c

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

var _ codegen.TestLauncher = (*Translator)(nil)

// LaunchTest implements codegen.TestLauncher: the program the build wrote to
// dir, compiled with the host's compiler beside dir, run with its stdin and
// stdout the test agent's JSON-RPC.
func (t *Translator) LaunchTest(ctx context.Context, dir string, _ codegen.LangTranslator, _ *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	bin := dir + ".testagent"
	if _, err := Host("").BuildDir(dir, bin, "c17"); err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, bin)
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
	cleanup := func() {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return &pipe{in: stdout, out: stdin}, cleanup, nil
}

// pipe is the agent's stdout and stdin as an RPCChannel.
type pipe struct {
	in  io.ReadCloser
	out io.WriteCloser
}

func (p *pipe) Read(b []byte) (int, error)  { return p.in.Read(b) }
func (p *pipe) Write(b []byte) (int, error) { return p.out.Write(b) }
func (p *pipe) Close() error {
	_ = p.out.Close()
	return p.in.Close()
}
