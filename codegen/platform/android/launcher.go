//go:build !js

package android

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LaunchTest implements codegen.TestLauncher. Dispatches on the
// testRunner option: robolectric → JVM build + stdio JSON-RPC;
// device → APK + adb forward + tcp JSON-RPC (Task 9).
func (g *Generator) LaunchTest(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	runner := codegen.OptionString(opts, "testRunner")
	if runner == "" {
		runner = "robolectric"
	}
	switch runner {
	case "robolectric":
		return g.launchRobolectric(ctx, dir, lang, opts)
	case "device":
		return g.launchDevice(ctx, dir, lang, opts)
	default:
		return nil, nil, fmt.Errorf("unknown testRunner %q", runner)
	}
}

// launchRobolectric drives the robolectric agent path. The android
// codegen has already emitted the full AGP scaffold into dir (the same
// scaffold the device path uses) plus a MainScreenAgentTest.kt JUnit
// class under app/src/test/kotlin. We:
//
//  1. Open a TCP listener on the loopback interface.
//  2. Invoke `./gradlew :app:testDebugUnitTest -Dsngl.agent.port=<N>`
//     so the JUnit @Test method can dial back to that listener once
//     Robolectric finishes spinning up the JVM-side Android runtime.
//  3. Accept the inbound connection and hand it to the driver as the
//     RPC channel.
//
// Coordination quirk: gradle spawns a forked JVM for the test task and
// passes -D system properties through; the JUnit body reads them with
// System.getProperty(). If the test class fails to compile or load,
// gradle exits without anyone dialling back — the listener's deadline
// (5 minutes, generous for cold dependency caches) catches that case.
func (g *Generator) launchRobolectric(ctx context.Context, dir string, _ codegen.LangTranslator, _ *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if !javaFound() {
		return nil, nil, &codegen.SkipError{Reason: "JDK 17+ not on PATH"}
	}
	if sdkRoot() == "" {
		return nil, nil, &codegen.SkipError{Reason: "ANDROID_HOME / ANDROID_SDK_ROOT not set"}
	}

	gradlew := filepath.Join(dir, "gradlew")
	if _, err := os.Stat(gradlew); err != nil {
		return nil, nil, &codegen.SkipError{Reason: "gradle wrapper not in emitted scaffold (expected gradlew)"}
	}
	_ = os.Chmod(gradlew, 0o755)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Buffer stderr from gradle in case the test fails before dialling
	// back — we surface the build log to the driver caller on accept
	// timeout to make diagnosis tractable.
	var buildLog bytes.Buffer
	cmd := exec.CommandContext(ctx, gradlew,
		":app:testDebugUnitTest",
		"--no-daemon", "--console=plain",
		"-Dsngl.agent.port="+strconv.Itoa(port),
		"--tests", "*.MainScreenAgentTest",
	)
	cmd.Dir = dir
	cmd.Stdout = &buildLog
	cmd.Stderr = &buildLog
	if err := cmd.Start(); err != nil {
		listener.Close()
		return nil, nil, fmt.Errorf("gradle start: %w", err)
	}

	// Watcher goroutine: if gradle exits before accept() fires, unblock
	// the accept by closing the listener and surface the build log.
	gradleDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(gradleDone)
	}()

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		// Wide deadline: cold gradle cache + robolectric runtime
		// download can take minutes on the first run.
		if tcp, ok := listener.(*net.TCPListener); ok {
			_ = tcp.SetDeadline(time.Now().Add(10 * time.Minute))
		}
		c, err := listener.Accept()
		accepted <- acceptResult{c, err}
	}()

	select {
	case r := <-accepted:
		if r.err != nil {
			_ = listener.Close()
			_ = cmd.Process.Kill()
			<-gradleDone
			fmt.Fprint(os.Stderr, buildLog.String())
			return nil, nil, fmt.Errorf("accept on agent port: %w", r.err)
		}
		cleanup := func() {
			_ = r.conn.Close()
			_ = listener.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-gradleDone
		}
		return r.conn, cleanup, nil
	case <-gradleDone:
		// Gradle exited before anyone dialled back. Close the listener
		// to unblock the accept goroutine, then surface the build log.
		_ = listener.Close()
		<-accepted
		fmt.Fprint(os.Stderr, buildLog.String())
		state := cmd.ProcessState
		if state != nil && state.ExitCode() != 0 {
			return nil, nil, fmt.Errorf("gradle :app:testDebugUnitTest exited %d before agent dialled back", state.ExitCode())
		}
		return nil, nil, fmt.Errorf("gradle :app:testDebugUnitTest exited before agent dialled back")
	}
}

func (g *Generator) launchDevice(ctx context.Context, dir string, lang codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	// SDK preconditions
	if !javaFound() {
		return nil, nil, &codegen.SkipError{Reason: "JDK 17+ not on PATH"}
	}
	if sdkRoot() == "" {
		return nil, nil, &codegen.SkipError{Reason: "ANDROID_HOME / ANDROID_SDK_ROOT not set"}
	}
	adb, err := androidTool("adb")
	if err != nil {
		return nil, nil, &codegen.SkipError{Reason: "adb not found in Android SDK"}
	}
	if _, err := androidTool("emulator"); err != nil {
		return nil, nil, &codegen.SkipError{Reason: "emulator not found in Android SDK"}
	}
	if _, err := pickAVD(); err != nil {
		return nil, nil, &codegen.SkipError{Reason: "no AVD configured (avdmanager create avd, or SNGL_AVD)"}
	}

	// The android scaffold has already been written into dir by
	// android.Generate (the standard flow under testMode=agent +
	// testRunner=device emits the full gradle/Android project layout,
	// including app/build.gradle.kts and src/main/...).
	// No additional layout work needed here — assembleDebug will pick up
	// the emitted .kt files directly.

	// Prefer the emitted gradle wrapper if present — it's a
	// self-bootstrapping shell script that downloads and caches a known
	// gradle distribution, so the host doesn't need system gradle.
	gradleBin := ""
	gradlew := filepath.Join(dir, "gradlew")
	if _, statErr := os.Stat(gradlew); statErr == nil {
		_ = os.Chmod(gradlew, 0o755)
		gradleBin = gradlew
	} else if sys, lookErr := exec.LookPath("gradle"); lookErr == nil {
		gradleBin = sys
	} else {
		return nil, nil, &codegen.SkipError{Reason: "gradle not on PATH and no gradlew in scaffold"}
	}

	var buildOut bytes.Buffer
	bld := exec.CommandContext(ctx, gradleBin, ":app:assembleDebug", "--no-daemon", "--console=plain")
	bld.Dir = dir
	bld.Stdout = &buildOut
	bld.Stderr = &buildOut
	if err := bld.Run(); err != nil {
		out := buildOut.String()
		fmt.Fprint(os.Stderr, out)
		return nil, nil, fmt.Errorf("gradle :app:assembleDebug: %w", err)
	}

	apk := filepath.Join(dir, "app", "build", "outputs", "apk", "debug", "app-debug.apk")
	if _, err := os.Stat(apk); err != nil {
		return nil, nil, fmt.Errorf("apk not found at %s: %w", apk, err)
	}

	// Bring up emulator if no device attached (reused from sngl run).
	if !hasDevice() {
		if err := ensureDevice(); err != nil {
			return nil, nil, &codegen.SkipError{Reason: fmt.Sprintf("failed to start emulator: %v", err)}
		}
	}

	// Install.
	if err := adbInstall(apk); err != nil {
		return nil, nil, fmt.Errorf("adb install: %w", err)
	}

	// Pick port + adb forward.
	hostPort, err := pickFreeLocalhostPort()
	if err != nil {
		return nil, nil, fmt.Errorf("pick port: %w", err)
	}
	devicePort := hostPort // use same port number on both sides

	// Defensive: remove any stale forward on this port (from a previous
	// crashed run).
	_ = exec.CommandContext(ctx, adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()

	fwd := exec.CommandContext(ctx, adb, "forward",
		fmt.Sprintf("tcp:%d", hostPort),
		fmt.Sprintf("tcp:%d", devicePort))
	fwd.Stderr = os.Stderr
	if err := fwd.Run(); err != nil {
		return nil, nil, fmt.Errorf("adb forward: %w", err)
	}

	// Derive package name from the emitted app's gradle config or fall
	// back to the convention used elsewhere. android codegen defaults
	// to "test.sngl.app" (per Config.withDefaults).
	pkg := codegen.OptionString(opts, "package")
	if pkg == "" {
		pkg = "test.sngl.app"
	}

	start := exec.CommandContext(ctx, adb, "shell", "am", "start",
		"-n", pkg+"/.MainActivity",
		"--ei", "SNGL_AGENT_PORT", fmt.Sprintf("%d", devicePort))
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		_ = exec.CommandContext(ctx, adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()
		return nil, nil, fmt.Errorf("am start: %w", err)
	}

	// Connect to forwarded port. adb forward succeeds on the local
	// dial even when the device-side listener isn't bound yet — it just
	// proxies the connection and the device end sees ECONNREFUSED.
	// Wait for the on-device ServerSocket to bind by polling `adb shell
	// netstat`; only then is dial→listener-accept reliable.
	if err := waitForDevicePortBound(ctx, adb, devicePort, 60*time.Second); err != nil {
		_ = exec.CommandContext(ctx, adb, "shell", "am", "force-stop", pkg).Run()
		_ = exec.CommandContext(ctx, adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()
		return nil, nil, fmt.Errorf("device agent did not bind port %d: %w", devicePort, err)
	}
	var conn net.Conn
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), 2*time.Second)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		_ = exec.CommandContext(ctx, adb, "shell", "am", "force-stop", pkg).Run()
		_ = exec.CommandContext(ctx, adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()
		return nil, nil, fmt.Errorf("device agent did not accept connection on %d", hostPort)
	}

	cleanup := func() {
		_ = conn.Close()
		_ = exec.Command(adb, "shell", "am", "force-stop", pkg).Run()
		_ = exec.Command(adb, "uninstall", pkg).Run()
		_ = exec.Command(adb, "forward", "--remove", fmt.Sprintf("tcp:%d", hostPort)).Run()
	}
	return conn, cleanup, nil
}

// findTestAgentPath returns the absolute path to pkg/kotlin/testagent
// in the sngl source tree. Used by the device + robolectric launchers
// to wire the testagent module into the synthesised gradle project via
// gradle composite-build (`includeBuild`).
//
// Lookup order:
//  1. $SNGL_HOST_GO_MOD: the script-test convention from Plan 1; its
//     parent dir is the sngl repo root.
//  2. Walk up from the running executable looking for a go.mod whose
//     module path is git.duckfam.us/jonathan/sngl.
//  3. Walk up from runtime.Caller(0)'s source path (works for `go test`
//     and `go run` where Executable() points at a build cache).
func findTestAgentPath() (string, error) {
	if mod := os.Getenv("SNGL_HOST_GO_MOD"); mod != "" {
		root := filepath.Dir(mod)
		p := filepath.Join(root, "pkg", "kotlin", "testagent")
		if _, err := os.Stat(filepath.Join(p, "build.gradle.kts")); err == nil {
			return p, nil
		}
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, exe)
	}
	if _, here, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, here)
	}
	for _, start := range candidates {
		dir := filepath.Dir(start)
		for i := 0; i < 12; i++ {
			modPath := filepath.Join(dir, "go.mod")
			if data, err := os.ReadFile(modPath); err == nil {
				if strings.Contains(string(data), "module git.duckfam.us/jonathan/sngl") {
					p := filepath.Join(dir, "pkg", "kotlin", "testagent")
					if _, err := os.Stat(filepath.Join(p, "build.gradle.kts")); err == nil {
						return p, nil
					}
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", &codegen.SkipError{Reason: "could not locate pkg/kotlin/testagent (set SNGL_HOST_GO_MOD)"}
}

// waitForDevicePortBound polls `adb shell` until something is listening
// on devicePort. Uses `cat /proc/net/tcp` because the emulator system
// image rarely ships `netstat` or `ss`. The procfs row's local-address
// field is host-byte-order hex (big-endian on most CPUs but emulators
// run x86_64), so we match against the little-endian hex of the port.
func waitForDevicePortBound(ctx context.Context, adb string, devicePort int, timeout time.Duration) error {
	// /proc/net/tcp formats local_address as "AABBCCDD:PPPP" where PPPP
	// is the port in hex. State 0A = LISTEN.
	portHex := fmt.Sprintf("%04X", devicePort)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.CommandContext(ctx, adb, "shell",
			"cat", "/proc/net/tcp", "/proc/net/tcp6").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				// Look for ":<hex-port> ... 0A " (LISTEN state).
				if strings.Contains(line, ":"+portHex+" ") && strings.Contains(line, " 0A ") {
					return nil
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %v", timeout)
}

// pickFreeLocalhostPort grabs a free local port by binding and closing.
// Brief race window between close and reuse — acceptable for test runs.
func pickFreeLocalhostPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
