package testutil

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/androidtc"
	"git.duckfam.us/jonathan/sngl/internal/jdk"
)

var snglBinaries sync.Map // owner -> func() (string, error)

// snglBinary builds `sngl` and returns its path, once per process. Callers
// shell out to it rather than to whatever `sngl` is on PATH, so the tests
// exercise the working tree and not the last `go install`.
//
// The output path is stable — a directory keyed by owner, the platform under
// test — rather than a fresh temp dir. `go build -o` relinks the ~84MB binary
// into every new location, so a per-run directory means one relink per test
// package per run; reusing the path lets Go skip the link entirely. Keying by
// owner keeps the platform test binaries out of each other's directories, so
// each deterministically reuses its own. codegen.BuildDir bounds and sweeps
// the directories, so this does not leak a binary per process the way an
// os.MkdirTemp did.
//
// The directory lock is deliberately never released: the binary is used for
// the lifetime of the test process, and releasing it would let another test
// binary claim the slot and wipe it mid-run. The OS drops the flock on exit.
func snglBinary(owner string) (string, error) {
	build := sync.OnceValues(func() (string, error) {
		root := projectRoot()
		dir, _, err := codegen.BuildDir("sngl-bin", root, owner)
		if err != nil {
			return "", err
		}
		bin := filepath.Join(dir, "sngl")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/sngl")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build sngl: %v\n%s", err, out)
		}
		return bin, nil
	})
	actual, _ := snglBinaries.LoadOrStore(owner, build)
	return actual.(func() (string, error))()
}

// snglEnv returns the environment for invoking the harness-built sngl binary.
//
// SNGL_NO_PROXY keeps main.proxyToGoTool from re-execing into `go tool sngl`:
// the harness deliberately built this binary from the working tree, and the
// proxy would both discard that and add two `go` invocations — about 160ms and
// 0.7s of CPU — to every call.
//
// GOMAXPROCS caps the `go build` this invocation will run: `go` takes its -p
// from GOMAXPROCS, so without this each of the fixtures holding a build token
// forks a further GOMAXPROCS compilers. The product is what exhausts memory,
// so the two factors are set together — see childProcs.
func snglEnv() []string {
	return append(os.Environ(), "SNGL_NO_PROXY=1",
		"GOMAXPROCS="+strconv.Itoa(childProcs()))
}

// childProcs divides the machine between the fixtures allowed to build at
// once, so their compilers together come to about one per core rather than
// one per core *each*.
func childProcs() int {
	return max(runtime.GOMAXPROCS(0)/codegen.BuildTokenSlots(), 1)
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

	bin, err := snglBinary(platform)
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
		skip, reason, err := ParseSkipCodegen(fixture)
		if err != nil {
			t.Fatalf("skip directive %s: %v", fixture, err)
		}
		if skip {
			t.Logf("%s: SKIP(codegen): %s", base, reason)
			continue
		}
		t.Run(base, func(t *testing.T) {
			// Fixtures are independent: each gets its own build
			// directory and its own subprocesses, so the only shared
			// state is the read-only sngl binary.
			t.Parallel()
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

// componentUnderTest reports the component every `func test…` in the fixture
// takes as its second parameter, or "" when they disagree or none does. Native
// mode builds one program and so can serve only one; the empty answer leaves
// such a fixture building the program as written.
func componentUnderTest(path string) string {
	src, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	found := ""
	for _, m := range testParamRe.FindAllStringSubmatch(string(src), -1) {
		if found != "" && found != m[1] {
			return ""
		}
		found = m[1]
	}
	return found
}

var testParamRe = regexp.MustCompile(`(?m)^func test[A-Za-z0-9_]*\([^,)]*,\s*[A-Za-z_][A-Za-z0-9_]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\)`)

// Declaration-only fixtures still run through generate; this lets the agent
// path short-circuit fixtures that have nothing to assert.
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

// Matches each platform's first SupportedLangs entry.
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

func runComponentNative(t *testing.T, snglBin, platform, fixture string) {
	t.Helper()

	if reason := nativeSkipReason(platform); reason != "" {
		t.Skip(reason)
	}
	if hasErrorDirective(fixture) {
		t.Skipf("fixture has ERROR directives — native target toolchain can't compile")
	}

	lang := langForPlatform(platform)

	// A stable, key-derived directory rather than t.TempDir(): the Go build
	// cache keys compile and link actions on the source directory, so a fresh
	// temp dir recompiles and relinks byte-identical generated code on every
	// run. See codegen.BuildDir.
	tmp, release, err := codegen.BuildDir("fixture-native", platform, lang, fixture)
	if err != nil {
		t.Fatalf("build dir: %v", err)
	}
	t.Cleanup(release)

	args := []string{"generate",
		"--lang=" + lang,
		"--platform=" + platform,
		"--opt", "test=true",
		"--opt", "main=true",
		"--out=" + tmp,
		fixture,
	}
	// The component under test is what the emitted test file is written
	// against, so this build makes it the root the way the launcher does:
	// otherwise it inlines into the fixture's own window and the Model carries
	// `x__inst0` where the test asks for `x`.
	if root := componentUnderTest(fixture); root != "" {
		args = append(args, "--opt", "rootComponent="+root)
	}
	cmd := exec.Command(snglBin, args...)
	cmd.Env = snglEnv()
	out, genErr := cmd.CombinedOutput()
	if genErr != nil {
		if reason, ok := unsupportedComponentReason(string(out)); ok {
			t.Skip(reason)
		}
		if reason, ok := skipReasonFromOutput(string(out)); ok {
			t.Skip(reason)
		}
		t.Fatalf("sngl generate: %v\n%s", genErr, out)
	}

	release, tokErr := codegen.AcquireBuildToken(t.Context())
	if tokErr != nil {
		t.Fatalf("build token: %v", tokErr)
	}
	defer release()

	if err := runNativeTarget(t, platform, tmp); err != nil {
		if se, ok := errors.AsType[*skipErr](err); ok {
			t.Skip(se.reason)
		}
		t.Fatalf("native target test: %v", err)
	}
}

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

	release, err := codegen.AcquireBuildToken(t.Context())
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	defer release()

	args := []string{"test", "--platform=" + platform, fixture}
	cmd := exec.Command(snglBin, args...)
	cmd.Env = snglEnv()
	out, err := cmd.CombinedOutput()
	outStr := string(out)
	if err != nil {
		if reason, ok := unsupportedComponentReason(outStr); ok {
			t.Skip(reason)
		}
		if reason, ok := skipReasonFromOutput(outStr); ok {
			t.Skip(reason)
		}
		t.Fatalf("sngl test failed: %v\n%s", err, outStr)
	}
	// A run in which every result was skipped prints SKIP where a real run
	// prints PASS, so the marker check below would read it as a failure.
	if strings.Contains(outStr, "\nSKIP\n") {
		t.Skipf("sngl test skipped every test:\n%s", outStr)
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

// unimplementedComponent matches the diagnostic a platform emits for a
// stdlib component it declares no implementation for.
var unimplementedComponent = regexp.MustCompile(`component "([^"]+)" has no (\S+) implementation`)

// Deliberately separate from skipReasonFromOutput, whose signals are all
// "this machine is missing a tool": that is a gap in the environment, this is
// a gap in the platform, and reading them off one list would let a genuinely
// broken toolchain hide as an unimplemented feature. The reason is the
// compiler's own diagnostic, which it derives from the platform package's
// declarations -- so a component gaining an implementation stops matching
// here on its own, with no list to update.
func unsupportedComponentReason(out string) (string, bool) {
	m := unimplementedComponent.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	return m[2] + " does not implement " + m[1], true
}

// "" when the platform participates in native mode (host toolchain compile + run).
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

// "" when the platform participates in agent mode (sngl test --platform=X).
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

// dir holds the just-emitted source. Returns *skipErr when the host toolchain
// reveals an unavailability better surfaced as a skip.
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

// Returns *skipErr when the output reveals a missing native dep (e.g. gtk4
// pkg-config).
func runGoTest(dir string) error {
	needTidy, err := codegen.WriteGoMod(dir, "", "")
	if err != nil {
		return err
	}
	tidy := func() error {
		out, err := codegen.TidyModule(context.Background(), dir)
		if err != nil {
			if reason, ok := skipReasonFromOutput(out); ok {
				return &skipErr{reason: reason}
			}
			return fmt.Errorf("go mod tidy: %v\n%s", err, out)
		}
		return nil
	}
	if needTidy {
		if err := tidy(); err != nil {
			return err
		}
	}

	run := func() (string, error) {
		test := exec.Command("go", "test", "-p", strconv.Itoa(childProcs()), "./...")
		test.Dir = dir
		out, err := test.CombinedOutput()
		return string(out), err
	}
	out, testErr := run()
	if testErr != nil && codegen.NeedsModuleTidy(out) {
		// The module graph seeded from the host did not cover this
		// program; let tidy resolve the rest and run once more.
		if err := tidy(); err != nil {
			return err
		}
		out, testErr = run()
	}
	if testErr != nil {
		if reason, ok := skipReasonFromOutput(out); ok {
			return &skipErr{reason: reason}
		}
		return fmt.Errorf("go test: %v\n%s", testErr, out)
	}
	return nil
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
