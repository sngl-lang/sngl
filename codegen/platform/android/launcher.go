//go:build !js

package android

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

func (g *Generator) launchRobolectric(ctx context.Context, dir string, _ codegen.LangTranslator, opts *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	if !javaFound() {
		return nil, nil, &codegen.SkipError{Reason: "JDK 17+ not on PATH"}
	}
	gradle, err := exec.LookPath("gradle")
	if err != nil {
		return nil, nil, &codegen.SkipError{Reason: "gradle not on PATH"}
	}

	// Package name matches Config default (see compiler_ir.go withDefaults
	// → "test.sngl.app"). If a future Config exposes the package as an
	// option we should plumb it through here instead of hardcoding.
	pkgName := codegen.OptionString(opts, "package")
	if pkgName == "" {
		pkgName = "test.sngl.app"
	}

	if err := writeRobolectricGradleProject(dir, pkgName); err != nil {
		return nil, nil, err
	}
	if err := moveEmittedKotlinIntoAppSrc(dir, pkgName); err != nil {
		return nil, nil, err
	}

	// gradle :app:installDist — produces an executable script in
	// app/build/install/app/bin/app
	var buildOut bytes.Buffer
	bld := exec.CommandContext(ctx, gradle, ":app:installDist", "--no-daemon", "--console=plain")
	bld.Dir = dir
	bld.Stdout = &buildOut
	bld.Stderr = &buildOut
	if err := bld.Run(); err != nil {
		out := buildOut.String()
		if strings.Contains(out, "us.duckfam.git.jonathan.sngl:testagent") &&
			strings.Contains(out, "Could not find") {
			return nil, nil, &codegen.SkipError{Reason: "pkg/kotlin/testagent not published to mavenLocal; run gradle publishToMavenLocal in pkg/kotlin/testagent/"}
		}
		fmt.Fprint(os.Stderr, out)
		return nil, nil, fmt.Errorf("gradle :app:installDist: %w", err)
	}

	binPath := filepath.Join(dir, "app", "build", "install", "app", "bin", "app")
	if _, err := os.Stat(binPath); err != nil {
		return nil, nil, fmt.Errorf("installDist output missing: %w", err)
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

	gradleBin, err := exec.LookPath("gradle")
	if err != nil {
		// Fall back to gradlew if the scaffold emitted one.
		gradlew := filepath.Join(dir, "gradlew")
		if _, statErr := os.Stat(gradlew); statErr == nil {
			gradleBin = gradlew
		} else {
			return nil, nil, &codegen.SkipError{Reason: "gradle not on PATH and no gradlew in scaffold"}
		}
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

	// Connect to forwarded port; the agent thread takes ~50-500ms after
	// am start to open its ServerSocket.
	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), time.Second)
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

// writeRobolectricGradleProject synthesises a gradle JVM application
// project at dir. The emitted .kt files will be moved into app/src/
// main/kotlin/<pkg-path>/ by moveEmittedKotlinIntoAppSrc.
func writeRobolectricGradleProject(dir, pkg string) error {
	settings := []byte(`rootProject.name = "snglroot"
include(":app")
`)
	if err := os.WriteFile(filepath.Join(dir, "settings.gradle.kts"), settings, 0o644); err != nil {
		return err
	}
	rootBuild := []byte(`plugins { kotlin("jvm") version "1.9.22" apply false }
`)
	if err := os.WriteFile(filepath.Join(dir, "build.gradle.kts"), rootBuild, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		return err
	}
	appBuild := []byte(`plugins {
    kotlin("jvm") version "1.9.22"
    application
}

repositories { mavenLocal(); mavenCentral(); google() }

dependencies {
    implementation("us.duckfam.git.jonathan.sngl:testagent:0.1.0")
    implementation("androidx.compose.ui:ui:1.6.0")
    implementation("androidx.compose.material:material:1.6.0")
    implementation("org.robolectric:robolectric:4.11.1")
    implementation("androidx.compose.ui:ui-test:1.6.0")
    implementation("androidx.compose.ui:ui-test-junit4:1.6.0")
}

application { mainClass.set("` + pkg + `.AgentMainKt") }

kotlin { jvmToolchain(17) }
`)
	return os.WriteFile(filepath.Join(dir, "app", "build.gradle.kts"), appBuild, 0o644)
}

// moveEmittedKotlinIntoAppSrc relocates the .kt files from dir's root
// into the gradle source layout app/src/main/kotlin/<pkg-path>/.
func moveEmittedKotlinIntoAppSrc(dir, pkg string) error {
	pkgPath := strings.ReplaceAll(pkg, ".", string(os.PathSeparator))
	dst := filepath.Join(dir, "app", "src", "main", "kotlin", pkgPath)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".kt") {
			continue
		}
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dst, name)); err != nil {
			return err
		}
	}
	return nil
}
