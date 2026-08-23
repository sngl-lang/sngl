package testutil

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/androidtc"
	"git.duckfam.us/jonathan/sngl/internal/jdk"
)

// snglBinary builds `sngl` into a directory the test framework removes, and
// returns its path. Callers shell out to it rather than to whatever `sngl` is
// on PATH, so the tests exercise the working tree and not the last
// `go install`.
//
// t.TempDir rather than os.MkdirTemp: the binary is ~84MB and this used to
// leak one per test process.
func snglBinary(t *testing.T) (string, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sngl")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/sngl")
	cmd.Dir = projectRoot()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build sngl: %v\n%s", err, out)
	}
	return bin, nil
}

// RunComponentFixtures executes every testdata/component_*.sngl fixture
// against the named platform in two modes:
//
//   - "native":     invokes `sngl generate --opt test=true ...` then
//     runs the emitted target-language test files via the host
//     toolchain (e.g. `go test ./...` for Go-emitting platforms).
//   - "agent":      invokes `sngl test --platform=<platform> ...`
//     which uses the platform's TestLauncher.
//
// Each fixture runs as a t.Run subtest so failures localize. When a
// mode's toolchain is missing on the host (no gradle, no Chromium,
// etc.), that mode reports t.Skip with a clear reason; the other mode
// still attempts. Platforms where a mode is intentionally not supported
// (e.g. html "native" mode is a no-op per Plan 4 spec) are t.Skip'd for
// that mode.
func RunComponentFixtures(t *testing.T, platform string) {
	t.Helper()

	bin, err := snglBinary(t)
	if err != nil {
		t.Fatalf("build sngl: %v", err)
	}

	root := projectRoot()
	testdataDir := filepath.Join(root, "testdata")
	matches, err := filepath.Glob(filepath.Join(testdataDir, "component_*.sngl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no component_*.sngl fixtures in %s", testdataDir)
	}

	for _, fixture := range matches {
		base := strings.TrimSuffix(filepath.Base(fixture), ".sngl")
		t.Run(base, func(t *testing.T) {
			fpath := fixture
			t.Run("native", func(t *testing.T) {
				runComponentNative(t, bin, platform, fpath)
			})
			t.Run("agent", func(t *testing.T) {
				runComponentAgent(t, bin, platform, fpath)
			})
		})
	}
}

// fixtureHasTestFunc reports whether the fixture contains at least one
// top-level `func test...` definition. Declaration-only fixtures still
// run through generate; this lets the agent path short-circuit fixtures
// that have nothing to assert.
func fixtureHasTestFunc(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "func test") {
			return true
		}
	}
	return false
}

// hasErrorDirective reports whether the fixture has any // ERROR(...)
// directives. Such fixtures intentionally contain build-time errors and
// can't be exercised through the platform's compile/run pipeline — the
// none-platform interpreter tolerates them but the native target
// toolchain will refuse to compile.
func hasErrorDirective(path string) bool {
	dirs, err := ParseDirectives(path)
	if err != nil {
		return false
	}
	return len(dirs) > 0
}

// langForPlatform returns the lang to pair with the platform for the
// native mode. Matches each platform's first SupportedLangs entry.
func langForPlatform(platform string) string {
	switch platform {
	case "android":
		return "kotlin"
	case "html":
		return "js"
	default:
		// bubbletea, fyne, gtk4
		return "go"
	}
}

// runComponentNative drives the "compile and run native target tests"
// path: `sngl generate --opt test=true` → run the host toolchain.
func runComponentNative(t *testing.T, snglBin, platform, fixture string) {
	t.Helper()

	if reason := nativeSkipReason(platform); reason != "" {
		t.Skip(reason)
	}
	if hasErrorDirective(fixture) {
		t.Skipf("fixture has ERROR directives — native target toolchain can't compile")
	}

	tmp := t.TempDir()
	lang := langForPlatform(platform)

	args := []string{"generate",
		"--lang=" + lang,
		"--platform=" + platform,
		"--opt", "test=true",
		"--opt", "main=true",
		"--out=" + tmp,
		fixture,
	}
	cmd := exec.Command(snglBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if reason, ok := skipReasonFromOutput(string(out)); ok {
			t.Skip(reason)
		}
		t.Fatalf("sngl generate: %v\n%s", err, out)
	}

	if err := runNativeTarget(t, platform, tmp); err != nil {
		var se *skipErr
		if errors.As(err, &se) {
			t.Skip(se.reason)
		}
		t.Fatalf("native target test: %v", err)
	}
}

// runComponentAgent drives the "sngl test --platform=X" path.
func runComponentAgent(t *testing.T, snglBin, platform, fixture string) {
	t.Helper()

	if reason := agentSkipReason(platform); reason != "" {
		t.Skip(reason)
	}
	if !fixtureHasTestFunc(fixture) {
		t.Skip("fixture has no func test… body — nothing for the agent to assert")
	}
	if hasErrorDirective(fixture) {
		// Some intentional-error fixtures can still produce useful
		// runtime test output, but matching ERROR directives requires
		// the in-process testrunner path used by none/. The exec-based
		// agent path treats any non-zero exit as a fail, so skip these.
		t.Skipf("fixture has ERROR directives — skip exec-based agent path")
	}

	args := []string{"test", "--platform=" + platform, fixture}
	cmd := exec.Command(snglBin, args...)
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err != nil {
		if reason, ok := skipReasonFromOutput(outStr); ok {
			t.Skip(reason)
		}
		t.Fatalf("sngl test failed: %v\n%s", err, outStr)
	}
	if !strings.Contains(outStr, "PASS") {
		t.Fatalf("sngl test produced no PASS marker:\n%s", outStr)
	}
}

// skipErr is internal to this package so callers can downgrade specific
// runNativeTarget failures to t.Skip.
type skipErr struct{ reason string }

func (s *skipErr) Error() string { return s.reason }

// skipReasonFromOutput matches a subset of common toolchain-missing
// messages that we'd prefer to surface as test skips rather than
// failures. Mirrors the SkipError patterns from the platform launchers.
func skipReasonFromOutput(out string) (string, bool) {
	signals := []struct {
		needle string
		reason string
	}{
		{"Chrome/Chromium not on PATH", "Chrome/Chromium not on PATH"},
		{"JDK 17+ not on PATH", "JDK 17+ not on PATH"},
		{"gradle not on PATH", "gradle not available"},
		{"ANDROID_HOME", "Android SDK not configured"},
		{"emulator not found", "emulator not found in Android SDK"},
		{"no AVD configured", "no AVD configured"},
		{"gtk4 dev libraries not installed", "gtk4 dev libraries not installed"},
		{"Package gtk4 was not found", "gtk4 dev libraries not installed"},
		{"No package 'gtk4' found", "gtk4 dev libraries not installed"},
		{"go not found in PATH", "go not on PATH"},
		// JDK/Gradle version incompatibility — the JDK selector should pin a
		// supported JDK, but if Gradle still rejects the runtime, skip rather
		// than fail on an environment mismatch.
		{"install JDK 21", "no compatible JDK for the Android build"},
		{"Could not determine java version", "JDK unsupported by bundled Gradle"},
		{"Unsupported class file major version", "JDK unsupported by bundled Gradle"},
	}
	for _, s := range signals {
		if strings.Contains(out, s.needle) {
			return s.reason, true
		}
	}
	return "", false
}

// nativeSkipReason reports a platform's static skip reason for the
// "native" mode (host toolchain compile + run), or "" if the platform
// participates.
func nativeSkipReason(platform string) string {
	switch platform {
	case "html":
		return "html has no native-target test toolchain (Plan 4: agent-mode only)"
	case "bubbletea", "fyne":
		if _, err := exec.LookPath("go"); err != nil {
			return "go not on PATH"
		}
		return ""
	case "gtk4":
		if _, err := exec.LookPath("go"); err != nil {
			return "go not on PATH"
		}
		if _, err := exec.LookPath("pkg-config"); err != nil {
			return "pkg-config not on PATH"
		}
		if err := exec.Command("pkg-config", "--exists", "gtk4").Run(); err != nil {
			return "gtk4 dev libraries not installed (pkg-config)"
		}
		if err := codegen.PlatformUnavailable("gtk4"); err != nil {
			return err.Error()
		}
		// gtk4's native test presents real GtkWindows via the snapshot
		// harness — skip when not inside a headless compositor so it doesn't
		// flash the desktop (see TestMain / MaybeReexecUnderCage).
		if r := GUIRenderSkipReason(); r != "" {
			return r
		}
		return ""
	case "android":
		if _, reason := jdk.CompatibleHome(androidtc.Default().JDKMin, androidtc.Default().JDKMax); reason != "" {
			return reason
		}
		if os.Getenv("ANDROID_HOME") == "" && os.Getenv("ANDROID_SDK_ROOT") == "" {
			return "ANDROID_HOME / ANDROID_SDK_ROOT not set"
		}
		return ""
	}
	return ""
}

// agentSkipReason reports a platform's static skip reason for the
// "agent" mode (sngl test --platform=X), or "" if the platform
// participates.
func agentSkipReason(platform string) string {
	switch platform {
	case "bubbletea", "fyne":
		if _, err := exec.LookPath("go"); err != nil {
			return "go not on PATH"
		}
		return ""
	case "gtk4":
		if _, err := exec.LookPath("go"); err != nil {
			return "go not on PATH"
		}
		if _, err := exec.LookPath("pkg-config"); err != nil {
			return "pkg-config not on PATH"
		}
		if err := exec.Command("pkg-config", "--exists", "gtk4").Run(); err != nil {
			return "gtk4 dev libraries not installed (pkg-config)"
		}
		if err := codegen.PlatformUnavailable("gtk4"); err != nil {
			return err.Error()
		}
		// Agent mode runs `sngl test --platform=gtk4`, which presents a real
		// GtkWindow for the snapshot — skip when not headless (see TestMain).
		if r := GUIRenderSkipReason(); r != "" {
			return r
		}
		return ""
	case "android":
		if _, reason := jdk.CompatibleHome(androidtc.Default().JDKMin, androidtc.Default().JDKMax); reason != "" {
			return reason
		}
		return ""
	case "html":
		// rod's launcher.LookPath walks PATH and several well-known
		// install locations. Defer to it rather than reimplementing
		// the search here.
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
			if _, err := exec.LookPath(name); err == nil {
				return ""
			}
		}
		return "Chrome/Chromium not on PATH"
	}
	return ""
}

// runNativeTarget runs the native target's test command in `dir` (the
// directory containing the just-emitted source). Returns *skipErr when
// the host toolchain reveals an unavailability we'd rather surface as a
// skip.
func runNativeTarget(t *testing.T, platform, dir string) error {
	t.Helper()
	switch platform {
	case "bubbletea", "fyne", "gtk4":
		return runGoTest(dir)
	case "android":
		return runGradleTest(dir)
	case "html":
		return &skipErr{reason: "html has no native-target test toolchain"}
	}
	return fmt.Errorf("no native-target runner wired for platform %s", platform)
}

// runGoTest synthesises go.mod (mirroring the launcher path), then runs
// `go mod tidy` and `go test ./...` in dir. Returns *skipErr when
// output reveals a missing native dep (e.g. gtk4 pkg-config).
func runGoTest(dir string) error {
	if err := writeTempGoMod(dir); err != nil {
		return err
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		if reason, ok := skipReasonFromOutput(string(out)); ok {
			return &skipErr{reason: reason}
		}
		return fmt.Errorf("go mod tidy: %v\n%s", err, out)
	}
	test := exec.Command("go", "test", "-trimpath", "./...")
	test.Dir = dir
	if out, err := test.CombinedOutput(); err != nil {
		if reason, ok := skipReasonFromOutput(string(out)); ok {
			return &skipErr{reason: reason}
		}
		return fmt.Errorf("go test: %v\n%s", err, out)
	}
	return nil
}

// writeTempGoMod synthesises go.mod for a temp go-target build dir.
// Mirrors codegen/lang/golang/launcher.go:writeTestGoMod so the helper
// emits the same module configuration the launcher would use, including
// any host-discovered replace directives.
func writeTempGoMod(dir string) error {
	goVersion, extra := codegen.DetectHostGoMod()
	if goVersion == "" {
		goVersion = "1.23"
	}
	mod := fmt.Sprintf("module tmp\n\ngo %s\n", goVersion)
	if extra != "" {
		mod += "\n" + extra
		if !strings.HasSuffix(mod, "\n") {
			mod += "\n"
		}
	}
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
}

// runGradleTest runs `./gradlew :app:testDebugUnitTest` in dir, pinned to a
// version-compatible JDK so a too-new ambient JDK doesn't fail the launch.
func runGradleTest(dir string) error {
	home, reason := jdk.CompatibleHome(androidtc.Default().JDKMin, androidtc.Default().JDKMax)
	if reason != "" {
		return &skipErr{reason: reason}
	}
	gradlew := filepath.Join(dir, "gradlew")
	if _, err := os.Stat(gradlew); err != nil {
		return &skipErr{reason: "no gradle wrapper in scaffold"}
	}
	_ = os.Chmod(gradlew, 0o755)
	cmd := exec.Command(gradlew, ":app:testDebugUnitTest", "--no-daemon", "--console=plain")
	cmd.Dir = dir
	cmd.Env = jdk.Env(home)
	if out, err := cmd.CombinedOutput(); err != nil {
		if reason, ok := skipReasonFromOutput(string(out)); ok {
			return &skipErr{reason: reason}
		}
		return fmt.Errorf("gradlew :app:testDebugUnitTest: %v\n%s", err, out)
	}
	return nil
}
