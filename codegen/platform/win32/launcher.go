//go:build !js

package win32

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/ir"
)

var _ codegen.TestLauncher = (*Generator)(nil)

// LaunchTest implements codegen.TestLauncher: the agent the build wrote to
// dir, cross-compiled with MinGW's compiler for its target and run -- under
// Wine, off Windows -- with the port of a loopback listener as its argument,
// which it connects back to and serves the driver over. Wine hands a Windows
// program none of the host's pipes, so the channel is TCP, as android's is.
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, _ *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if id := lang.LanguageIdentifier(); id != "c" {
		return nil, nil, fmt.Errorf("win32: testing in %s is not written yet", id)
	}
	if ok, reason := g.ProbeTest(); !ok {
		return nil, nil, &codegen.SkipError{Reason: reason}
	}
	cc, err := clang.ForTarget(clang.TargetOf(dir), "")
	if err != nil {
		return nil, nil, &codegen.SkipError{Reason: err.Error()}
	}
	bin := dir + ".testagent"
	if _, err := cc.BuildDir(dir, bin, "c17"); err != nil {
		return nil, nil, err
	}
	argv := clang.Launch(cc.Exe(bin))
	env := os.Environ()
	if argv[0] == "wine" {
		prefix, err := winePrefix(ctx)
		if err != nil {
			return nil, nil, err
		}
		env = wineEnv(prefix)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	cmd := exec.CommandContext(ctx, argv[0], append(argv[1:], strconv.Itoa(port))...)
	cmd.Dir = dir
	cmd.Env = env
	var log bytes.Buffer
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		listener.Close()
		return nil, nil, fmt.Errorf("start %s: %w", argv[0], err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	type accept struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan accept, 1)
	go func() {
		// Wide: a prefix Wine has not yet started a server for takes
		// seconds to come up.
		_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(2 * time.Minute))
		c, err := listener.Accept()
		accepted <- accept{c, err}
	}()
	select {
	case a := <-accepted:
		listener.Close()
		if a.err != nil {
			_ = cmd.Process.Kill()
			<-exited
			return nil, nil, fmt.Errorf("the test agent did not connect: %w\n%s", a.err, log.String())
		}
		cleanup := func() {
			// The agent ends as its channel closes; one that does not is
			// stopped.
			_ = a.conn.Close()
			select {
			case <-exited:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-exited
			}
		}
		return a.conn, cleanup, nil
	case <-exited:
		listener.Close()
		<-accepted
		return nil, nil, fmt.Errorf("the test agent exited before it connected (%s)\n%s", cmd.ProcessState, log.String())
	}
}

// wineEnv is the environment a program runs under Wine in: the prefix, no
// display -- the prefix draws through Wine's null driver, so no window
// reaches the developer's desktop -- and none of Wine's own diagnostics.
func wineEnv(prefix string) []string {
	var env []string
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "DISPLAY", "WAYLAND_DISPLAY", "WINEPREFIX", "WINEDEBUG", "WINEDLLOVERRIDES":
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"WINEPREFIX="+prefix,
		"WINEDEBUG=-all",
		// Nothing a test runs asks for .NET or a browser engine, which a
		// fresh prefix would offer to install, nor wants menu entries.
		"WINEDLLOVERRIDES=mscoree,mshtml=;winemenubuilder.exe=d",
	)
}

// winePrefix is the Wine prefix every test agent runs in, made the first
// time one is asked for and kept in the cache, since one takes seconds to
// make: its windows drawn by the null driver, which needs no display and
// still draws each into a bitmap PrintWindow can read.
func winePrefix(ctx context.Context) (string, error) {
	prefix := filepath.Join(codegen.SnglCacheDir(), "wine")
	ready := filepath.Join(prefix, ".sngl-ready")
	if _, err := os.Stat(ready); err == nil {
		return prefix, nil
	}
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		return "", err
	}
	unlock, err := lockPath(prefix + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := os.Stat(ready); err == nil {
		return prefix, nil
	}
	// A prefix whose making was cut short fails to start until it is made
	// again from nothing.
	if err := os.RemoveAll(prefix); err != nil {
		return "", err
	}
	env := wineEnv(prefix)
	steps := [][]string{
		{"wineboot", "-i"},
		{"wine", "reg", "add", `HKCU\Software\Wine\Drivers`, "/v", "Graphics", "/d", "null", "/f"},
		// The registry reaches the disk as the server ends.
		{"wineserver", "-w"},
	}
	for _, step := range steps {
		cmd := exec.CommandContext(ctx, step[0], step[1:]...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) || len(out) > 0 {
				return "", fmt.Errorf("making the Wine prefix %s: %s: %w\n%s", prefix, strings.Join(step, " "), err, out)
			}
			return "", fmt.Errorf("making the Wine prefix %s: %s: %w", prefix, strings.Join(step, " "), err)
		}
	}
	if err := os.WriteFile(ready, nil, 0o644); err != nil {
		return "", err
	}
	return prefix, nil
}
