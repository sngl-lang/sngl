package testutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/txtar"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testlaunch"
	"duckfam.us/sngl/internal/trust"
)

// RunComponentFixtures runs the tests every testdata/component_*.txtar fixture
// declares on platform, as `sngl test --platform=<platform>` would.
//
// The Go platforms and android compile and run a fixture's emitted tests in
// the golden's `run/` record. html's record is `node --check`, and its
// assertions only run when something drives a browser, which is this: each
// launch is answered from a `testrun/` record in the fixture's archive, keyed
// by a digest of the page and every file served beside it. update reruns a
// launch whose digest moved and rewrites its record, and verify reruns them all.
func RunComponentFixtures(t *testing.T, platform string, update, verify bool) {
	t.Helper()

	plat := codegen.LookupPlatform(platform)
	if plat == nil {
		t.Fatalf("unknown platform %q", platform)
	}
	var lang codegen.LangTranslator
	for _, name := range plat.SupportedLangs() {
		if lang = codegen.LookupLang(name); lang != nil {
			break
		}
	}

	matches, err := filepath.Glob(filepath.Join(projectRoot(), "testdata", "component_*.txtar"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no component_*.txtar fixtures in testdata")
	}

	for _, archive := range matches {
		base := strings.TrimSuffix(filepath.Base(archive), ".txtar")
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			runComponentFixture(t, archive, base, plat, lang, update, verify)
		})
	}
}

func runComponentFixture(t *testing.T, archive, base string, plat codegen.PlatformGenerator, lang codegen.LangTranslator, update, verify bool) {
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	arc := txtar.Parse(raw)
	records, rest, err := testlaunch.SplitArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	src, err := archiveSource(rest)
	if err != nil {
		t.Fatalf("%s: %v", base, err)
	}
	if !strings.Contains(src, "\nfunc test") {
		t.Skip("fixture declares no func test… — nothing for the runner to assert")
	}
	dir := t.TempDir()
	file := base + ".sngl"
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if hasErrorDirective(path) {
		t.Skipf("fixture has ERROR directives — it does not build")
	}

	doc, err := parser.Parse(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	resolver := build.NewResolver(dir)
	resolver.Trust = trust.AllowAll()
	pkg, err := build.Check(doc, build.CheckConfig{
		Dir:      dir,
		Resolver: resolver,
		IsMain:   true,
		Targets:  build.SelectedTargets(testlaunch.LangIdent(lang), plat.PlatformIdentifier()),
	})
	if err != nil {
		if reason, ok := unsupportedComponentReason(err.Error()); ok {
			t.Skip(reason)
		}
		t.Fatal(err)
	}
	pkg.SourcePath = path

	if update || verify {
		release, err := codegen.AcquireBuildToken(t.Context())
		if err != nil {
			t.Fatalf("build token: %v", err)
		}
		defer release()
	}
	runs := testlaunch.NewRecords(records, update, verify)
	results, err := testlaunch.Run(t.Context(), pkg, testlaunch.Config{
		Platform: plat,
		Lang:     lang,
		Trust:    trust.AllowAll(),
		Dir:      dir,
		File:     file,
		Records:  runs,
	})
	if missing, ok := errors.AsType[*codegen.UnimplementedComponent](err); ok {
		t.Skipf("%s does not implement %s", missing.Platform, missing.Component)
	}
	if err != nil {
		t.Fatal(err)
	}
	ran := reportResults(t, results)
	if update && !t.Failed() {
		if err := testlaunch.WriteArchive(archive, arc, rest, runs.Written()); err != nil {
			t.Fatal(err)
		}
	}
	if ran == 0 {
		t.Skip("every test skipped")
	}
}

// reportResults fails t for each failed result and returns how many ran.
func reportResults(t *testing.T, results []*codegen.TestResult) int {
	ran := 0
	for _, r := range results {
		if r.Skipped {
			continue
		}
		ran++
		if !r.Passed {
			t.Errorf("%s: %s\n%s", r.Desc, r.Error, strings.Join(r.Log, "\n"))
		}
		ran += reportResults(t, r.Children)
	}
	return ran
}

// archiveSource returns the fixture's own .sngl source -- everything outside
// `out/` and `run/`, which are the golden and the record.
func archiveSource(files []txtar.File) (string, error) {
	var b strings.Builder
	for _, f := range files {
		name := strings.TrimPrefix(f.Name, "./")
		if strings.HasPrefix(name, "out/") || strings.HasPrefix(name, "run/") {
			continue
		}
		if !strings.HasSuffix(name, ".sngl") {
			continue
		}
		b.Write(f.Data)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("archive has no .sngl source")
	}
	return b.String(), nil
}

var testParamRe = regexp.MustCompile(`(?m)^func test[A-Za-z0-9_]*\([^,)]*,\s*[A-Za-z_][A-Za-z0-9_]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\)`)

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

// TestHarnessBuild reports the build options a fixture carrying `func test…`
// needs, or ok=false when it carries none.
//
// The two facts are read off the source rather than written in a directive,
// because the source already states them. That a fixture has tests is that it
// declares one; which component they are written against is the second
// parameter every one of them takes. The component-fixture harness derived
// both this way long before a golden did, and exporting it is what lets the
// two agree rather than each deriving its own answer.
//
// `test` and `main` are what turn a generated program into one the host
// toolchain can run: the first emits the companion test files, the second an
// entry point for them to sit beside. `rootComponent` makes the component
// under test the root the way the launcher does -- otherwise it inlines into
// the fixture's own window and the Model carries `x__inst0` where the test
// asks for `x`.
func TestHarnessBuild(source string) (opts map[string]string, ok bool) {
	if !strings.Contains(source, "\nfunc test") && !strings.HasPrefix(source, "func test") {
		return nil, false
	}
	opts = map[string]string{"test": "true"}
	if root := componentUnderTestIn(source); root != "" {
		opts["rootComponent"] = root
	}
	return opts, true
}

// componentUnderTestIn reports the component every `func test…` takes as its
// second parameter, or "" when they disagree or none does. One build serves
// one root, so a fixture whose tests disagree is built as written.
func componentUnderTestIn(source string) string {
	found := ""
	for _, m := range testParamRe.FindAllStringSubmatch(source, -1) {
		if found != "" && found != m[1] {
			return ""
		}
		found = m[1]
	}
	return found
}
