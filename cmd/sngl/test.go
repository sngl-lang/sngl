package main

import (
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
	// userNamed tracks files the user named directly on the command line
	// (vs walked from a directory or `...` glob). Parse/check failures on
	// user-named files are real errors; failures on walked files that are
	// clearly not test files (no `test*` function) are silently skipped
	// because such files are typically `error_*.sngl` checker fixtures
	// that intentionally fail to check.
	userNamed := explicitFileSet(args)

	runFilter, _ := cmd.Flags().GetString("run")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")

	if platform == "all" {
		return runTestAll(language, files, explicitFiles, userNamed, runFilter, verbose, format)
	}

	plat := codegen.LookupPlatform(platform)
	if plat == nil {
		return fmt.Errorf("unknown platform: %s", platform)
	}
	runner, ok := plat.(codegen.TestRunner)
	if !ok {
		return fmt.Errorf("platform %q does not support testing", platform)
	}

	lang, err := resolveTestLang(plat, language)
	if err != nil {
		return err
	}

	totalTests, totalFail, allResults := runOnPlatform(runner, lang, files, explicitFiles, userNamed, runFilter)

	return reportResults(allResults, totalTests, totalFail, format, verbose)
}

// runTestAll fans out across every registered platform that implements
// TestRunner. Platforms whose TestProber reports unavailable are skipped
// with a SKIP banner; available platforms run the full file set and their
// results are aggregated.
func runTestAll(language string, files []string, explicitFiles, userNamed map[string]bool, runFilter string, verbose bool, format string) error {
	names := codegen.Platforms()
	var ran, skipped, failedPlats int
	var grandTests, grandFail int
	var allResults []*codegen.TestResult

	for _, name := range names {
		plat := codegen.LookupPlatform(name)
		runner, ok := plat.(codegen.TestRunner)
		if !ok {
			continue
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
		tests, fails, results := runOnPlatform(runner, lang, files, explicitFiles, userNamed, runFilter)
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
// and returns aggregated counts plus the per-test results. Files in
// userNamed are required to parse/check successfully; failures on
// non-userNamed files (walked from a directory or glob) are silently
// skipped when the file has no top-level test functions.
func runOnPlatform(runner codegen.TestRunner, lang codegen.LangTranslator, files []string, explicitFiles, userNamed map[string]bool, runFilter string) (int, int, []*codegen.TestResult) {
	var totalTests, totalFail int
	var allResults []*codegen.TestResult

	for _, filename := range files {
		absFilename, _ := filepath.Abs(filename)
		named := userNamed[absFilename]
		tf, err := os.Open(filename)
		if err != nil {
			if named {
				fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
				totalFail++
			}
			continue
		}
		start := time.Now()
		doc, err := parseSNGL(filename, tf)
		tf.Close()
		if err != nil {
			if named {
				fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
				totalFail++
			}
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
			if !named {
				continue
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}
		slog.Info("check", "file", filename, "duration", time.Since(start))

		results, err := safeRunTests(runner, pkg, lang)
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
func safeRunTests(runner codegen.TestRunner, pkg *ir.Package, lang codegen.LangTranslator) (results []*codegen.TestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test runner: %v", r)
		}
	}()
	return runner.RunTests(pkg, lang)
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
