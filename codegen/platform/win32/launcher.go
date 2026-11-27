//go:build !js

package win32

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

var _ codegen.TestLauncher = (*Generator)(nil)

// LaunchTest implements codegen.TestLauncher: the agent the build wrote to
// dir, cross-compiled with MinGW's compiler for its target and run -- under
// Wine, off Windows -- with the port of a loopback listener as its argument,
// which it connects back to and serves the driver over. Wine hands a Windows
// program none of the host's pipes, so the channel is TCP, as android's is.
// C compiles at the level opts' `optimize` names.
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if ok, reason := g.ProbeTest(); !ok {
		return nil, nil, &codegen.SkipError{Reason: reason}
	}
	cc, err := clang.ForTarget(clang.TargetOf(dir), "")
	if err != nil {
		return nil, nil, &codegen.SkipError{Reason: err.Error()}
	}
	bin := dir + ".testagent"
	if lang.LanguageIdentifier() == "go" {
		// Go's toolchain calls the same compiler, through cgo.
		if bin, err = golang.BuildModule(dir, bin); err != nil {
			return nil, nil, err
		}
	} else {
		if _, err := cc.BuildDir(dir, bin, "c17", codegen.OptionString(opts, "optimize")); err != nil {
			return nil, nil, err
		}
		bin = cc.Exe(bin)
	}
	argv := clang.Launch(bin)
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
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := StderrToFile(cmd)
	if err != nil {
		listener.Close()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		listener.Close()
		stderr()
		return nil, nil, fmt.Errorf("start %s: %w", argv[0], err)
	}
	// What the agent wrote, read once it has exited.
	var log strings.Builder
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		log.WriteString(stdout.String() + stderr())
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

var _ codegen.ProgramRunner = (*Generator)(nil)

// RunProgram implements codegen.ProgramRunner: the program the build wrote
// to dir, cross-compiled for its target -- in its language, as LaunchTest
// does -- and run once per run -- under Wine,
// off Windows, in the prefix the test agent runs in -- for no more than a
// minute each.
func (g *Generator) RunProgram(ctx context.Context, dir string, runs []codegen.ProgramRun) (string, []codegen.ProgramRan, error) {
	if ok, reason := g.ProbeTest(); !ok {
		return "", nil, &codegen.SkipError{Reason: reason}
	}
	cc, err := clang.ForTarget(clang.TargetOf(dir), "")
	if err != nil {
		return "", nil, &codegen.SkipError{Reason: err.Error()}
	}
	bin := filepath.Join(dir, "app")
	// The build wrote main.go where its language is Go, and main.c where C.
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err == nil {
		if bin, err = golang.BuildModule(dir, bin); err != nil {
			return "", nil, err
		}
	} else {
		if out, err := cc.BuildDir(dir, bin, "c17", ""); err != nil {
			return out, nil, err
		}
		bin = cc.Exe(bin)
	}
	argv := clang.Launch(bin)
	env := os.Environ()
	if argv[0] == "wine" {
		prefix, err := winePrefix(ctx)
		if err != nil {
			return "", nil, err
		}
		env = wineEnv(prefix)
	}
	var ran []codegen.ProgramRan
	for _, r := range runs {
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		cmd := exec.CommandContext(rctx, argv[0], append(argv[1:], r.Args...)...)
		cmd.WaitDelay = 5 * time.Second
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdin = bytes.NewReader(r.Stdin)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		stderr, err := StderrToFile(cmd)
		if err != nil {
			cancel()
			return "", nil, err
		}
		err = cmd.Run()
		cancel()
		// Windows' C library writes a text stream's newline as CRLF.
		lf := strings.NewReplacer("\r\n", "\n")
		ran = append(ran, codegen.ProgramRan{Stdout: lf.Replace(stdout.String()), Stderr: lf.Replace(stderr()), Err: err})
	}
	return "", ran, nil
}

// WineEnv is the environment a Windows program runs under Wine in, in the
// prefix every test agent runs in (winePrefix): a test running one of its
// own takes it, since Wine's default prefix, made on the first run, offers
// to install what a test wants none of and waits for an answer.
func WineEnv(ctx context.Context) ([]string, error) {
	prefix, err := winePrefix(ctx)
	if err != nil {
		return nil, err
	}
	return wineEnv(prefix), nil
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
	// Named for the steps below, so a branch taking other steps never runs
	// in, or removes, a prefix another branch made.
	prefix := filepath.Join(codegen.SnglCacheDir(), "wine-2")
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
		// Snapshot records were drawn in 8pt; Wine 11.22 defaults to 9pt.
		{"wine", "reg", "add", `HKCU\Control Panel\Desktop\WindowMetrics`, "/v", "MessageFont", "/t", "REG_BINARY", "/d", messageFont, "/f"},
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

// messageFont is the LOGFONTW of 8pt Tahoma at 96 DPI, as reg add takes
// binary: a height of -11, weight 400, DEFAULT_CHARSET, the face padded to
// LF_FACESIZE.
var messageFont = "f5ffffff" + strings.Repeat("00", 12) + "90010000" + "00000001" + "00000000" +
	"5400610068006f006d006100" + strings.Repeat("00", 64-12)

// StderrToFile points cmd's stderr, a program Wine runs, at a file, and is
// what was written to it, which also closes it. The first program of a
// Wine session starts the session's own -- services.exe, explorer.exe
// /desktop and the rest -- and they inherit its stderr, holding it open
// until the server ends, seconds after the program has. A pipe there keeps
// exec's Wait waiting on them, so a run that cold-started a session under
// load failed with exec.ErrWaitDelay, its output complete; a file is done
// with as the program exits.
func StderrToFile(cmd *exec.Cmd) (read func() string, err error) {
	f, err := os.CreateTemp("", "sngl-stderr-*")
	if err != nil {
		return nil, err
	}
	cmd.Stderr = f
	return func() string {
		defer os.Remove(f.Name())
		defer f.Close()
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err.Error()
		}
		b, err := io.ReadAll(f)
		if err != nil {
			return err.Error()
		}
		return string(b)
	}, nil
}
