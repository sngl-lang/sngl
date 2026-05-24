//go:build !js

package android

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

func (g *Generator) launchDevice(_ context.Context, _ string, _ codegen.LangTranslator, _ *ir.StructLit) (codegen.RPCChannel, codegen.Cleanup, error) {
	return nil, nil, fmt.Errorf("testRunner=device: not yet implemented")
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
