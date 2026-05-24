package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/none"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/spf13/cobra"
)

var testCmd = &cobra.Command{
	Use:   "test [file|dir...]",
	Short: "Run SNGL tests",
	Args:  cobra.ArbitraryArgs,
	RunE:  runTest,
}

func init() {
	testCmd.Flags().String("run", "", "filter tests by description pattern")
	testCmd.Flags().String("format", "text", "output format: text or json")
	testCmd.Flags().String("platform", "", "target platform (e.g. html)")
	testCmd.Flags().String("language", "", "target language (e.g. js)")
	testCmd.Flags().StringSlice("opt", nil, "generator options (key=value)")
}

// parseTestOpts converts the --opt key=value slice into a SNGL StructLit
// suitable for codegen.ApplyOptions. Unlike compile, test does not
// validate against per-target option schemas — unknown keys are passed
// through and individual platforms ignore what they don't recognize.
func parseTestOpts(raw []string) *ir.StructLit {
	if len(raw) == 0 {
		return nil
	}
	m := make(map[string]any, len(raw))
	for _, entry := range raw {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	return codegen.OptionsFromMap(m)
}

func runTest(cmd *cobra.Command, args []string) error {
	platform, _ := cmd.Flags().GetString("platform")
	language, _ := cmd.Flags().GetString("language")

	if platform == "" {
		platform = "none"
	}

	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}
	// Test mode: every discovered fixture is standalone. Sibling-merging
	// only makes sense when the user names a single file from a real
	// package; for `sngl test ./...` or `sngl test testdata/`, merging
	// would cascade intentional-error fixtures into every other file.
	explicitFiles := make(map[string]bool, len(files))
	for _, f := range files {
		abs, _ := filepath.Abs(f)
		explicitFiles[abs] = true
	}

	runFilter, _ := cmd.Flags().GetString("run")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")
	opts := parseTestOpts(optSlice)

	if platform == "all" {
		return runTestAll(language, files, explicitFiles, runFilter, verbose, format, opts)
	}

	plat := codegen.LookupPlatform(platform)
	if plat == nil {
		return fmt.Errorf("unknown platform: %s", platform)
	}
	lang, err := resolveTestLang(plat, language)
	if err != nil {
		return err
	}

	runner, _ := plat.(codegen.TestRunner)
	if runner == nil && resolveLauncher(plat, lang) == nil {
		return fmt.Errorf("platform %q does not support testing", platform)
	}

	totalTests, totalFail, allResults := runOnPlatform(cmd.Context(), plat, runner, lang, files, explicitFiles, runFilter, opts)

	return reportResults(allResults, totalTests, totalFail, format, verbose)
}

// runTestAll fans out across every registered platform that implements
// TestRunner. Platforms whose TestProber reports unavailable are skipped
// with a SKIP banner; available platforms run the full file set and their
// results are aggregated.
func runTestAll(language string, files []string, explicitFiles map[string]bool, runFilter string, verbose bool, format string, opts *ir.StructLit) error {
	names := codegen.Platforms()
	var ran, skipped, failedPlats int
	var grandTests, grandFail int
	var allResults []*codegen.TestResult

	for _, name := range names {
		plat := codegen.LookupPlatform(name)
		runner, _ := plat.(codegen.TestRunner)
		// Skip if neither legacy runner nor launcher path is available.
		// Lang-level launcher resolution requires the lang, looked up below.
		if runner == nil {
			// resolveLauncher needs lang; resolve provisionally to see if
			// a lang launcher exists. If lang lookup fails we'll handle
			// it normally below; here just skip if both paths are absent.
			lang, _ := resolveTestLang(plat, language)
			if resolveLauncher(plat, lang) == nil {
				continue
			}
		}
		if prober, ok := plat.(codegen.TestProber); ok {
			if okp, reason := prober.ProbeTest(); !okp {
				fmt.Printf("--- SKIP platform=%s: %s\n", name, reason)
				skipped++
				continue
			}
		}
		lang, err := resolveTestLang(plat, language)
		if err != nil {
			fmt.Fprintf(os.Stderr, "--- platform=%s: %s\n", name, err)
			failedPlats++
			continue
		}
		fmt.Printf("=== platform=%s\n", name)
		tests, fails, results := runOnPlatform(context.Background(), plat, runner, lang, files, explicitFiles, runFilter, opts)
		ran++
		grandTests += tests
		grandFail += fails
		for _, r := range results {
			r.Component = name + "/" + r.Component
			allResults = append(allResults, r)
		}
	}

	if ran == 0 && skipped > 0 {
		fmt.Println()
		fmt.Printf("ALL SKIPPED (%d platform(s) unavailable)\n", skipped)
		return nil
	}

	if err := reportResults(allResults, grandTests, grandFail, format, verbose); err != nil {
		return err
	}
	if failedPlats > 0 {
		return fmt.Errorf("%d platform(s) failed to start", failedPlats)
	}
	return nil
}

// resolveTestLang picks the LangTranslator for a test invocation. An
// explicit --language flag wins; otherwise the platform's first
// SupportedLang is used. A platform with no supported langs (e.g. `none`)
// is fine and returns nil.
func resolveTestLang(plat codegen.PlatformGenerator, language string) (codegen.LangTranslator, error) {
	if language != "" {
		lang := codegen.LookupLang(language)
		if lang == nil {
			return nil, fmt.Errorf("unknown language: %s", language)
		}
		return lang, nil
	}
	for _, name := range plat.SupportedLangs() {
		if l := codegen.LookupLang(name); l != nil {
			return l, nil
		}
	}
	return nil, nil
}

// runOnPlatform executes all test files against a single platform runner
// and returns aggregated counts plus the per-test results. Parse and
// check failures count as test failures — `./...` already skips
// `testdata/` and `.`/`_`-prefixed directories, so walked files are
// expected to be valid SNGL.
func runOnPlatform(ctx context.Context, plat codegen.PlatformGenerator, runner codegen.TestRunner, lang codegen.LangTranslator, files []string, explicitFiles map[string]bool, runFilter string, opts *ir.StructLit) (int, int, []*codegen.TestResult) {
	if ctx == nil {
		ctx = context.Background()
	}
	var totalTests, totalFail int
	var allResults []*codegen.TestResult

	for _, filename := range files {
		absFilename, _ := filepath.Abs(filename)
		tf, err := os.Open(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}
		start := time.Now()
		doc, err := parseSNGL(filename, tf)
		tf.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}
		slog.Info("parse", "file", filename, "duration", time.Since(start))

		if len(doc.TestFuncs()) == 0 {
			continue
		}

		if !explicitFiles[absFilename] {
			start = time.Now()
			doc = mergeDir(doc, filename)
			slog.Info("merge", "file", filename, "duration", time.Since(start))
		}

		// Filter tests by --run pattern before checking so filtered-out
		// tests don't cause spurious type errors.
		if runFilter != "" {
			var filtered []ast.Stmt
			for _, stmt := range doc.Stmts {
				fn, ok := stmt.(*ast.FuncDef)
				if !ok {
					filtered = append(filtered, stmt)
					continue
				}
				if fn.IsTest() && strings.Contains(fn.Name, runFilter) {
					filtered = append(filtered, fn)
				} else if !fn.IsTest() {
					filtered = append(filtered, fn)
				}
			}
			doc.Stmts = filtered
		}

		if len(doc.TestFuncs()) == 0 {
			continue
		}

		start = time.Now()
		pkg, err := checkDoc(doc, filepath.Dir(filename), true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}
		slog.Info("check", "file", filename, "duration", time.Since(start))

		// Prefer the TestLauncher path whenever the platform/lang pair
		// resolves a launcher (today: any go-backed platform). Otherwise
		// fall back to the legacy runner.RunTests path (e.g. `none`).
		var results []*codegen.TestResult
		if resolveLauncher(plat, lang) != nil {
			results, err = runViaLauncher(ctx, plat, lang, pkg, opts, filepath.Dir(filename))
		} else {
			results, err = safeRunTests(runner, pkg, lang, opts)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}

		for _, r := range results {
			allResults = append(allResults, r)
			totalTests += countTests(r)
			if !r.Passed {
				totalFail++
			}
		}
	}

	return totalTests, totalFail, allResults
}

// safeRunTests calls runner.RunTests and converts a panic into an error so
// a single buggy fixture on one platform doesn't take down the whole
// matrix. Useful when running --platform=all across many fixtures.
func safeRunTests(runner codegen.TestRunner, pkg *ir.Package, lang codegen.LangTranslator, opts *ir.StructLit) (results []*codegen.TestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test runner: %v", r)
		}
	}()
	return runner.RunTests(pkg, lang, opts)
}

// reportResults prints aggregated test output in either text or JSON form
// and returns a non-nil error when any test failed. Shared between
// single-platform and --platform=all paths.
func reportResults(allResults []*codegen.TestResult, totalTests, totalFail int, format string, verbose bool) error {
	if format == "json" {
		return printJSON(allResults, totalTests, totalFail)
	}

	for _, r := range allResults {
		printResult(r, r.Component, verbose)
	}

	fmt.Println()
	if totalFail > 0 {
		fmt.Printf("FAIL\n")
		fmt.Printf("not ok   %d tests, %d failures\n", totalTests, totalFail)
		return fmt.Errorf("test failed")
	}
	fmt.Printf("PASS\n")
	fmt.Printf("ok   %d tests, 0 failures\n", totalTests)
	return nil
}

func printResult(r *codegen.TestResult, prefix string, verbose bool) {
	name := prefix + "/" + r.Desc

	if verbose {
		fmt.Printf("=== RUN   %s\n", name)
	}

	if r.Passed {
		fmt.Printf("--- PASS: %s (%.2fs)\n", name, r.Duration.Seconds())
	} else {
		fmt.Printf("--- FAIL: %s (%.2fs)\n", name, r.Duration.Seconds())
		if r.Error != "" {
			for line := range strings.SplitSeq(r.Error, "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}
	if verbose {
		for _, line := range r.Log {
			fmt.Printf("    %s\n", line)
		}
	}

	for _, child := range r.Children {
		printResult(child, name, verbose)
	}
}

func countTests(r *codegen.TestResult) int {
	n := 1
	for _, child := range r.Children {
		n += countTests(child)
	}
	return n
}

type jsonOutput struct {
	Passed   bool         `json:"passed"`
	Tests    int          `json:"tests"`
	Failures int          `json:"failures"`
	Results  []jsonResult `json:"results"`
}

type jsonResult struct {
	Name     string       `json:"name"`
	Passed   bool         `json:"passed"`
	Error    string       `json:"error,omitempty"`
	Log      []string     `json:"log,omitempty"`
	Duration float64      `json:"duration_s"`
	Children []jsonResult `json:"children,omitempty"`
}

func printJSON(results []*codegen.TestResult, totalTests, totalFail int) error {
	out := jsonOutput{
		Passed:   totalFail == 0,
		Tests:    totalTests,
		Failures: totalFail,
	}
	for _, r := range results {
		out.Results = append(out.Results, toJSON(r, r.Component))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	if totalFail > 0 {
		return fmt.Errorf("test failed")
	}
	return nil
}

func toJSON(r *codegen.TestResult, prefix string) jsonResult {
	name := prefix + "/" + r.Desc
	jr := jsonResult{
		Name:     name,
		Passed:   r.Passed,
		Error:    r.Error,
		Log:      r.Log,
		Duration: r.Duration.Seconds(),
	}
	for _, child := range r.Children {
		jr.Children = append(jr.Children, toJSON(child, name))
	}
	return jr
}
