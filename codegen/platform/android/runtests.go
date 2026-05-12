//go:build !js

package android

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ProbeTest reports whether the host can run Compose UI tests under
// Robolectric via Gradle. Test execution needs:
//   - a JDK 17+ on PATH (Robolectric + Compose require it)
//   - Gradle reachable (either system `gradle` or a generated
//     `gradlew` — the runner emits a wrapper when missing)
//   - an Android SDK rooted at $ANDROID_HOME with the matching
//     platform jar (same probe build/run already perform)
//
// Compose UI tests are heavyweight; first run downloads Gradle deps
// and warms the Kotlin daemon, so callers should expect minutes on a
// cold cache.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("java"); err != nil {
		return false, "java not on PATH (Compose UI tests need JDK 17+)"
	}
	if root := sdkRoot(); root == "" {
		return false, "ANDROID_HOME / ANDROID_SDK_ROOT not set (Compose tests need android.jar)"
	}
	if _, err := resolveToolchain(); err != nil {
		return false, fmt.Sprintf("android toolchain probe failed: %v", err)
	}
	return true, ""
}

// RunTests is the entry point for `sngl test --platform=android`.
//
// Each test group (one per component-typed param) gets its own temp
// Gradle Android project, with:
//   - the component promoted into a synthetic window
//   - emitted Compose source in test-mode (hoisted MainScreenState)
//   - a Kotlin test class under src/test/kotlin/ driving Robolectric
//   - Compose UI test deps wired into app/build.gradle.kts
//
// Gradle runs `:app:testDebugUnitTest`; results parse from
// build/test-results/testDebugUnitTest/*.xml.
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator, opts *ir.StructLit) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	if lang.LanguageIdentifier() != "kotlin" {
		return nil, fmt.Errorf("android RunTests: only --lang kotlin is supported (got %q)", lang.LanguageIdentifier())
	}

	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return nil, fmt.Errorf("android RunTests options: %w", err)
	}
	cfg = cfg.withDefaults()

	doc := ir.Convert(pkg)
	astTestFuncs := doc.TestFuncs()
	if len(astTestFuncs) == 0 {
		return nil, nil
	}
	groups := testharness.Group(astTestFuncs)
	var compGroups []testharness.TestGroup
	for _, gr := range groups {
		if gr.Component != "" {
			compGroups = append(compGroups, gr)
		}
	}
	if len(compGroups) == 0 {
		return nil, nil
	}

	var results []*codegen.TestResult
	for _, group := range compGroups {
		compDoc := testharness.Promote(doc, group.Component)
		if compDoc == nil {
			continue
		}
		compPkg, diags := checker.Check(compDoc, &checker.Config{IsMain: true})
		hasErr := false
		for _, d := range diags {
			if d.Severity == ir.Error {
				hasErr = true
				break
			}
		}
		if hasErr || compPkg == nil {
			continue
		}
		caps := g.Capabilities().Merge(lang.Capabilities())
		if err := lower.Lower(compPkg, caps, lower.Options{}); err != nil {
			return nil, fmt.Errorf("android lower %q: %w", group.Component, err)
		}

		grpResults, err := runAndroidTestGroup(pkg, compPkg, group, cfg)
		if err != nil {
			return nil, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

func runAndroidTestGroup(origPkg, compPkg *ir.Package, group testharness.TestGroup, cfg Config) ([]*codegen.TestResult, error) {
	dir, err := os.MkdirTemp("", "sngl-android-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	if os.Getenv("SNGL_KEEP_TEST_DIR") == "" {
		defer os.RemoveAll(dir)
	} else {
		fmt.Fprintln(os.Stderr, "sngl-android-test temp dir:", dir)
	}

	// 1) Scaffold the Gradle Android project in test mode.
	// Templates use {{skip}} to opt out conditionally; honour
	// ErrSkip from WriteTo as "don't emit this file".
	for _, f := range scaffoldTestFiles(cfg, false) {
		var buf bytes.Buffer
		if _, err := f.WriteTo(&buf); err != nil {
			if errors.Is(err, codegen.ErrSkip) {
				continue
			}
			return nil, fmt.Errorf("buffer %s: %w", f.Name, err)
		}
		out := filepath.Join(dir, f.Name)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", out, err)
		}
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", out, err)
		}
	}

	// 2) Compile the promoted component into Compose source
	// (test-mode: MainScreenState hoisted) under app/src/main/java.
	//
	// Promote() drops the target component but leaves siblings as
	// ComponentDecls; the android codegen would otherwise emit each
	// as a @Composable fun whose body references state that doesn't
	// exist on MainScreenState. Test rendering only needs the
	// synthetic window, so clear the sibling components.
	compPkg.Components = nil
	ctx := codegen.NewCodegenCtx(&codegen.Request{Pkg: compPkg}, "android")
	src, err := CompileTestIR(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("compile test source for %q: %w", group.Component, err)
	}
	pkgPath := pkgToPath(cfg.Package)
	mainKt := filepath.Join(dir, "app", "src", "main", "java", pkgPath, "MainScreen.kt")
	if err := os.MkdirAll(filepath.Dir(mainKt), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(mainKt, src, 0o644); err != nil {
		return nil, fmt.Errorf("write MainScreen.kt: %w", err)
	}

	// 3) Emit the Kotlin test class. Conditional/loop ids gated by
	// `if`/`for` go through Compose finder helpers; everything else
	// reads off MainScreenState directly.
	methodFields := testharness.CollectConditionalIDs(compPkg)
	testKt, err := emitAndroidTestSource(cfg, origPkg, group, methodFields)
	if err != nil {
		return nil, err
	}
	testPath := filepath.Join(dir, "app", "src", "test", "kotlin", pkgPath,
		exportName(group.Component)+"Test.kt")
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(testPath, []byte(testKt), 0o644); err != nil {
		return nil, fmt.Errorf("write test source: %w", err)
	}

	// 4) Run Gradle's testDebugUnitTest task. A non-zero exit can
	// mean either a real build failure (compile errors) OR just
	// "tests failed" — Gradle conflates them. Always look for
	// JUnit XML first: if it exists we have per-test results;
	// only synthesise build-failure entries when no results were
	// written.
	gradleErr := runGradleTests(dir)
	results, parseErr := parseAndroidTestResults(dir, group)
	if parseErr == nil && len(results) > 0 {
		return results, nil
	}
	if gradleErr != nil {
		return buildFailureResults(group, gradleErr), nil
	}
	if parseErr != nil {
		return buildFailureResults(group, parseErr), nil
	}
	return nil, nil
}

// emitAndroidTestSource builds the Kotlin source for one group:
// package + imports + class header + one @Test per SNGL test func.
// methodFields names ids gated by `if`/`for` so the test lowering
// can route those through Compose finders instead of state reads.
func emitAndroidTestSource(cfg Config, origPkg *ir.Package, group testharness.TestGroup, methodFields map[string]bool) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", cfg.Package)
	b.WriteString("import androidx.compose.ui.test.assertTextEquals\n")
	b.WriteString("import androidx.compose.ui.test.junit4.createComposeRule\n")
	b.WriteString("import androidx.compose.ui.test.junit4.ComposeContentTestRule\n")
	b.WriteString("import androidx.compose.ui.test.onAllNodesWithTag\n")
	b.WriteString("import androidx.compose.ui.test.onNodeWithTag\n")
	b.WriteString("import androidx.compose.ui.test.performClick\n")
	b.WriteString("import androidx.compose.ui.test.performTextReplacement\n")
	b.WriteString("import androidx.compose.ui.semantics.SemanticsProperties\n")
	b.WriteString("import androidx.compose.ui.semantics.getOrNull\n")
	b.WriteString("import org.junit.Rule\n")
	b.WriteString("import org.junit.Test\n")
	b.WriteString("import org.junit.runner.RunWith\n")
	b.WriteString("import org.robolectric.RobolectricTestRunner\n")
	b.WriteString("import org.robolectric.annotation.Config\n\n")

	// Helpers used by lowered assertions:
	//   composeNodeText     – textual content of a single tagged node.
	//   composeNodeTextAt   – textual content of the Nth tagged node
	//                         (for `for`-loop refs where the same id
	//                         maps to multiple widgets).
	//   composeNodeCount    – count of nodes matching a tag; non-zero
	//                         maps to "present" for `c.<id> != null`.
	b.WriteString("private fun composeNodeText(rule: ComposeContentTestRule, tag: String): String {\n")
	b.WriteString("    val node = rule.onNodeWithTag(tag).fetchSemanticsNode()\n")
	b.WriteString("    val text = node.config.getOrNull(SemanticsProperties.Text)?.joinToString(\"\") { ann -> ann.text } ?: \"\"\n")
	b.WriteString("    val edit = node.config.getOrNull(SemanticsProperties.EditableText)?.text\n")
	b.WriteString("    return edit ?: text\n")
	b.WriteString("}\n\n")
	b.WriteString("private fun composeNodeTextAt(rule: ComposeContentTestRule, tag: String, index: Int): String {\n")
	b.WriteString("    val node = rule.onAllNodesWithTag(tag)[index].fetchSemanticsNode()\n")
	b.WriteString("    val text = node.config.getOrNull(SemanticsProperties.Text)?.joinToString(\"\") { ann -> ann.text } ?: \"\"\n")
	b.WriteString("    val edit = node.config.getOrNull(SemanticsProperties.EditableText)?.text\n")
	b.WriteString("    return edit ?: text\n")
	b.WriteString("}\n\n")
	b.WriteString("private fun composeNodeCount(rule: ComposeContentTestRule, tag: String): Int {\n")
	b.WriteString("    return rule.onAllNodesWithTag(tag).fetchSemanticsNodes().size\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "@RunWith(RobolectricTestRunner::class)\n")
	fmt.Fprintf(&b, "@Config(sdk = [33])\n")
	fmt.Fprintf(&b, "class %sTest {\n", exportName(group.Component))
	b.WriteString("    @get:Rule val composeTestRule = createComposeRule()\n\n")

	for _, tf := range group.Funcs {
		var fn *ir.Func
		for _, f := range origPkg.Funcs {
			if f.Name == tf.Name {
				fn = f
				break
			}
		}
		if fn == nil {
			continue
		}
		suffix := strings.TrimPrefix(fn.Name, "test")
		b.WriteString(kotlin.LowerTestFunc(fn, suffix, methodFields))
	}

	b.WriteString("}\n")
	return b.String(), nil
}

func runGradleTests(dir string) error {
	gradle := filepath.Join(dir, "gradlew")
	if info, err := os.Stat(gradle); err != nil {
		sys, lookErr := exec.LookPath("gradle")
		if lookErr != nil {
			return fmt.Errorf("neither gradlew nor gradle found")
		}
		gradle = sys
	} else if info.Mode()&0o111 == 0 {
		_ = os.Chmod(gradle, 0o755)
	}
	cmd := exec.Command(gradle, ":app:testDebugUnitTest", "--no-daemon", "--console=plain")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gradle testDebugUnitTest: %w\n%s", err, out.String())
	}
	return nil
}

// buildFailureResults synthesises one TestResult per declared test
// func when Gradle itself fails (compile error, dep resolution, etc.).
// The same error string appears on every entry so the runner reports a
// failure for each rather than silently dropping the whole group.
func buildFailureResults(group testharness.TestGroup, err error) []*codegen.TestResult {
	msg := err.Error()
	out := make([]*codegen.TestResult, 0, len(group.Funcs))
	for _, tf := range group.Funcs {
		out = append(out, &codegen.TestResult{
			Component: group.Component,
			Desc:      tf.Name,
			Passed:    false,
			Error:     msg,
		})
	}
	return out
}

// parseAndroidTestResults reads Gradle's JUnit XML and maps each
// `<testcase>` entry to a codegen.TestResult. Failures attach the
// failure message + stack trace as Error / Log.
func parseAndroidTestResults(dir string, group testharness.TestGroup) ([]*codegen.TestResult, error) {
	resultsDir := filepath.Join(dir, "app", "build", "test-results", "testDebugUnitTest")
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", resultsDir, err)
	}
	var out []*codegen.TestResult
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "TEST-") || !strings.HasSuffix(e.Name(), ".xml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(resultsDir, e.Name()))
		if err != nil {
			continue
		}
		var ts junitTestSuite
		if err := xml.Unmarshal(raw, &ts); err != nil {
			continue
		}
		for _, tc := range ts.Cases {
			r := &codegen.TestResult{
				Component: group.Component,
				Desc:      tc.Name,
				Passed:    tc.Failure == nil && tc.Error == nil,
			}
			if tc.Failure != nil {
				r.Error = strings.TrimSpace(tc.Failure.Message + "\n" + tc.Failure.Body)
			} else if tc.Error != nil {
				r.Error = strings.TrimSpace(tc.Error.Message + "\n" + tc.Error.Body)
			}
			out = append(out, r)
		}
	}
	return out, nil
}

type junitTestSuite struct {
	XMLName xml.Name        `xml:"testsuite"`
	Cases   []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name    string        `xml:"name,attr"`
	Failure *junitFailure `xml:"failure"`
	Error   *junitFailure `xml:"error"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}
