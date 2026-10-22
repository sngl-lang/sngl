// Package toolchain builds generated code with the host's own compiler, and
// runs it when it carries tests.
//
// It is the half of a fixture that a golden cannot state. A golden says what
// the compiler emitted; only a host toolchain says whether that emission is a
// program. Both claims matter and they fail differently -- a codegen change
// that reorders two statements breaks the golden, and one that emits a name
// nothing declares leaves the golden looking fine.
//
// # Why this is a package rather than a harness's own code
//
// Three harnesses drove a host toolchain over generated code and each had its
// own copy of "can this machine do it": internal/testutil's component
// fixtures, cmd/sngl's script conditions, and the platform snapshot tests.
// They disagreed. testutil's agent path asked rod's browser names for html
// while cmd/sngl asked rod itself, and testutil's native path required a
// headless compositor for gtk4 where cmd/sngl required only a display -- so a
// fixture could run under one harness and skip under the other for reasons
// neither stated.
//
// The availability question and the running of it are therefore one
// implementation, asked in one vocabulary: a lang and a platform, the pair a
// build target is.
package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/androidtc"
	"duckfam.us/sngl/internal/headless"
	"duckfam.us/sngl/internal/jdk"
)

// Unavailable reports why this host cannot build generated <lang>/<platform>
// code, or "" when it can.
//
// It is deliberately a *host* question and not a platform one: whether gtk4
// can generate at all is codegen.PlatformUnavailable's answer, which this
// folds in for the platforms that need it.
func Unavailable(lang, platform string) string {
	switch platform {
	case "bubbletea", "fyne", "html", "none":
		// html's generated artifact is a page and a script; `--lang go`
		// makes it a Go server, which is the only half a compiler sees.
		if lang == "go" || platform != "html" {
			if reason := needGo(); reason != "" {
				return reason
			}
		}
		if lang == "js" || lang == "none" {
			return needNode()
		}
		return ""
	case "gtk4":
		if reason := needGo(); reason != "" {
			return reason
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
	return "no host toolchain wired for platform " + platform
}

// PresentsWindows reports why a <lang>/<platform> run cannot present the real
// windows it wants to, or "" when it can.
//
// Separate from Unavailable because it is a different kind of missing: the
// compiler is there and the code builds, and what is absent is somewhere to
// draw.
//
// It asks only whether there is *anywhere* to draw, which is not what
// headless.SkipReason answers. That reports the absence of cage even when a
// desktop is right there, because a test suite should not throw windows onto
// the user's screen -- a politeness rule, and the right one for a test that
// may run at any time. Read as a capability here it would refuse to verify a
// gtk4 fixture on any developer machine without cage installed, which is a
// skip dressed as an error: the tooling is present and the build would run.
// `go tool verify` still wraps the whole suite in a compositor, so the
// courtesy is kept where it was always enforced.
func PresentsWindows(platform string) string {
	if platform != "gtk4" {
		return ""
	}
	if headless.Active() || headless.HasDisplay() || headless.Compositor() != "" {
		return ""
	}
	return "no display and no headless compositor: a gtk4 program has nowhere to draw"
}

// DrawsHere reports why a <platform> program this process starts has nowhere
// to draw, or "" when it has.
//
// Narrower than PresentsWindows: a test binary wraps itself in cage, so for
// it an installed compositor is somewhere to draw, but nothing wraps a
// program `sngl test` starts -- it draws in this process's display or in the
// cage this process is already inside. A cage installed that will not start
// (CI's container) is neither, and gtk_init there ends the program as soon as
// its first test opens a window, which reads as every test failing.
func DrawsHere(platform string) string {
	if platform != "gtk4" {
		return ""
	}
	if headless.Active() || headless.HasDisplay() {
		return ""
	}
	return "no display and not inside a headless compositor: a gtk4 program has nowhere to draw"
}

func needGo() string {
	if _, err := exec.LookPath("go"); err != nil {
		return "go not on PATH"
	}
	return ""
}

func needNode() string {
	if _, err := exec.LookPath("node"); err != nil {
		return "node not on PATH"
	}
	return ""
}

// SkipError is a failure the caller should report as a skip: the host revealed
// a missing tool partway through, which no up-front probe had asked about.
type SkipError struct{ Reason string }

func (s *SkipError) Error() string { return s.Reason }

// Build compiles the generated files for one target, and runs whatever tests
// they carry. It returns the toolchain's combined output whether or not it
// succeeded -- a failure is only diagnosable with it, and a success is worth
// logging when a caller asks.
//
// files is written into a directory keyed by its own content, so byte-identical
// output lands where the last run left its build cache.
func Build(ctx context.Context, files map[string][]byte, lang, platform string) (output string, err error) {
	dir, release, err := codegen.BuildDirForContent("fixture", files, lang, platform)
	if err != nil {
		return "", err
	}
	defer release()

	for name, data := range files {
		if err := writeUnder(dir, name, data); err != nil {
			return "", err
		}
	}

	token, err := codegen.AcquireBuildToken(ctx)
	if err != nil {
		return "", err
	}
	defer token()

	switch platform {
	case "bubbletea", "fyne", "gtk4":
		return goTest(ctx, dir)
	case "html":
		if lang == "go" {
			return goTest(ctx, dir)
		}
		return nodeCheck(ctx, dir, files)
	case "android":
		return gradleTest(ctx, dir)
	}
	return "", fmt.Errorf("no host toolchain wired for platform %s", platform)
}

// goTest builds and runs a generated Go module. `go test ./...` rather than
// `go build ./...` because it is both at once: a package with no _test.go
// files is compiled and reported `[no test files]`, so one command covers a
// target whose fixture carries tests and one whose fixture does not.
func goTest(ctx context.Context, dir string) (string, error) {
	return goModule(ctx, dir, "test", "-p", strconv.Itoa(childProcs()), "./...")
}

// goModule runs the go command in a generated module, writing its go.mod
// first and tidying it where the module graph seeded from the host does not
// cover the program.
func goModule(ctx context.Context, dir string, args ...string) (string, error) {
	needTidy, err := codegen.WriteGoMod(dir, "", "")
	if err != nil {
		return "", err
	}
	tidy := func() (string, error) {
		out, err := codegen.TidyModule(ctx, dir)
		if err != nil {
			if reason, ok := SkipReason(out); ok {
				return out, &SkipError{Reason: reason}
			}
			return out, fmt.Errorf("go mod tidy: %w", err)
		}
		return out, nil
	}
	if needTidy {
		if out, err := tidy(); err != nil {
			return out, err
		}
	}

	run := func() (string, error) {
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		cmd.Env = env()
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, runErr := run()
	if runErr != nil && codegen.NeedsModuleTidy(out) {
		if tout, err := tidy(); err != nil {
			return out + tout, err
		}
		out, runErr = run()
	}
	if runErr != nil {
		if reason, ok := SkipReason(out); ok {
			return out, &SkipError{Reason: reason}
		}
		return out, fmt.Errorf("go %s: %w", args[0], runErr)
	}
	return out, nil
}

// A Run is one execution of the program Exec builds.
type Run struct {
	Args  []string
	Stdin []byte
	// Wayland asks for the program's Wayland protocol log, on its stderr.
	Wayland bool
}

// Ran is what one Run did. Err is set when the program could not start, did
// not finish in time or exited non-zero.
type Ran struct {
	Stdout, Stderr string
	Err            error
}

// runTimeout bounds one execution: every program a fixture runs ends itself.
const runTimeout = time.Minute

// Exec builds the generated files for one Go target into a program and runs
// it once per run, in the display this process has, which for a program that
// presents windows is the headless compositor its test binary runs inside.
func Exec(ctx context.Context, files map[string][]byte, lang, platform string, runs []Run) (output string, ran []Ran, err error) {
	if lang != "go" {
		return "", nil, fmt.Errorf("no way to run a %s/%s program", lang, platform)
	}
	dir, release, err := codegen.BuildDirForContent("fixture", files, lang, platform)
	if err != nil {
		return "", nil, err
	}
	defer release()
	for name, data := range files {
		if err := writeUnder(dir, name, data); err != nil {
			return "", nil, err
		}
	}
	bin, err := os.MkdirTemp("", "sngl-exec")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(bin)
	program := filepath.Join(bin, "app")

	token, err := codegen.AcquireBuildToken(ctx)
	if err != nil {
		return "", nil, err
	}
	output, err = goModule(ctx, dir, "build", "-o", program, ".")
	token()
	if err != nil {
		return output, nil, err
	}

	for _, r := range runs {
		rctx, cancel := context.WithTimeout(ctx, runTimeout)
		cmd := exec.CommandContext(rctx, program, r.Args...)
		// A helper the program started -- a D-Bus session bus -- can hold its
		// stderr open after it is killed, and Wait would wait for it.
		cmd.WaitDelay = 5 * time.Second
		cmd.Dir = bin
		cmd.Env = os.Environ()
		if r.Wayland {
			cmd.Env = append(cmd.Env, "WAYLAND_DEBUG=1")
		}
		cmd.Stdin = bytes.NewReader(r.Stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		ran = append(ran, Ran{Stdout: stdout.String(), Stderr: stderr.String(), Err: err})
	}
	return output, ran, nil
}

// nodeCheck parses every emitted script with node.
//
// It is a syntax check and not a run: a page's script expects a DOM, so
// executing it outside a browser reports the absence of `document` rather than
// anything about the program. Driving a real browser is the html platform's
// own snapshot harness, and this is the cheap check every build can afford.
//
// The scripts are mostly *inline*. `--lang none` emits one self-contained
// index.html per window, so a check that looked only at .js files found
// nothing to do and passed -- a vacuous record, which is worse than no record,
// since it reads in the archive exactly like a real one. Every <script> body
// in an emitted page is extracted and checked as its own classic script, which
// is what the platform emits: no fixture in the tree writes a module.
func nodeCheck(ctx context.Context, dir string, files map[string][]byte) (string, error) {
	var out strings.Builder
	checked := 0
	check := func(name string, src []byte) error {
		path := name + ".check.js"
		if err := writeUnder(dir, path, src); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, "node", "--check", path)
		cmd.Dir = dir
		cmd.Env = env()
		b, err := cmd.CombinedOutput()
		out.Write(b)
		checked++
		if err != nil {
			return fmt.Errorf("node --check %s: %w", name, err)
		}
		return nil
	}
	for _, name := range sortedNames(files) {
		switch {
		case strings.HasSuffix(name, ".js"):
			if err := check(name, files[name]); err != nil {
				return out.String(), err
			}
		case strings.HasSuffix(name, ".html"):
			for i, src := range inlineScripts(files[name]) {
				if err := check(fmt.Sprintf("%s.%d", name, i), src); err != nil {
					return out.String(), err
				}
			}
		}
	}
	if checked == 0 {
		// Every html build emits a script. None means the extraction stopped
		// matching what the platform writes, and silence there is what this
		// function was rewritten to stop reporting as a pass.
		return out.String(), fmt.Errorf("no script found in %d emitted files; nothing was checked", len(files))
	}
	return out.String(), nil
}

// inlineScripts returns the body of every <script> element that carries one.
// A tag with a src and no body is someone else's file and has nothing to
// parse.
func inlineScripts(page []byte) [][]byte {
	var out [][]byte
	rest := string(page)
	for {
		i := strings.Index(rest, "<script")
		if i < 0 {
			return out
		}
		rest = rest[i:]
		j := strings.Index(rest, ">")
		if j < 0 {
			return out
		}
		open, body := rest[:j], rest[j+1:]
		k := strings.Index(body, "</script>")
		if k < 0 {
			return out
		}
		if !strings.Contains(open, " src=") && strings.TrimSpace(body[:k]) != "" {
			out = append(out, []byte(body[:k]))
		}
		rest = body[k:]
	}
}

func sortedNames(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// gradleTest runs the scaffold's own wrapper, pinned to a JDK inside the
// window this Gradle can launch -- a too-new ambient JDK fails the launch
// rather than the build.
func gradleTest(ctx context.Context, dir string) (string, error) {
	home, reason := jdk.CompatibleHome(androidtc.Default().JDKMin, androidtc.Default().JDKMax)
	if reason != "" {
		return "", &SkipError{Reason: reason}
	}
	gradlew := dir + "/gradlew"
	if _, err := os.Stat(gradlew); err != nil {
		return "", &SkipError{Reason: "no gradle wrapper in scaffold"}
	}
	_ = os.Chmod(gradlew, 0o755)
	cmd := exec.CommandContext(ctx, gradlew, ":app:testDebugUnitTest", "--no-daemon", "--console=plain")
	cmd.Dir = dir
	cmd.Env = jdk.Env(home)
	b, err := cmd.CombinedOutput()
	if err != nil {
		if r, ok := SkipReason(string(b)); ok {
			return string(b), &SkipError{Reason: r}
		}
		return string(b), fmt.Errorf("gradlew :app:testDebugUnitTest: %w", err)
	}
	return string(b), nil
}

func env() []string {
	return append(os.Environ(), "SNGL_NO_PROXY=1", "GOMAXPROCS="+strconv.Itoa(childProcs()))
}

// childProcs divides the machine between the fixtures allowed to build at
// once, so their compilers together come to about one per core rather than one
// per core each.
func childProcs() int {
	return max(runtime.GOMAXPROCS(0)/codegen.BuildTokenSlots(), 1)
}

func writeUnder(dir, name string, data []byte) error {
	path := dir + "/" + name
	if i := strings.LastIndex(path, "/"); i >= 0 {
		if err := os.MkdirAll(path[:i], 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
