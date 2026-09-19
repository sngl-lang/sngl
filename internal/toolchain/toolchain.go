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
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/androidtc"
	"git.duckfam.us/jonathan/sngl/internal/headless"
	"git.duckfam.us/jonathan/sngl/internal/jdk"
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
// draw. A caller that only builds ignores this; one that runs does not.
func PresentsWindows(platform string) string {
	if platform != "gtk4" {
		return ""
	}
	return headless.SkipReason()
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
		cmd := exec.CommandContext(ctx, "go", "test", "-p", strconv.Itoa(childProcs()), "./...")
		cmd.Dir = dir
		cmd.Env = env()
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, testErr := run()
	if testErr != nil && codegen.NeedsModuleTidy(out) {
		// The module graph seeded from the host did not cover this program;
		// let tidy resolve the rest and run once more.
		if tout, err := tidy(); err != nil {
			return out + tout, err
		}
		out, testErr = run()
	}
	if testErr != nil {
		if reason, ok := SkipReason(out); ok {
			return out, &SkipError{Reason: reason}
		}
		return out, fmt.Errorf("go test: %w", testErr)
	}
	return out, nil
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
