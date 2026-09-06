package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform/none"
	"git.duckfam.us/jonathan/sngl/internal/build"
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

// Unlike compile, test validates nothing against the per-target option
// schemas: an unknown key passes through for the platform to ignore.
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

	units, err := resolveUnits(args)
	if err != nil {
		return err
	}
	if len(units) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	runFilter, _ := cmd.Flags().GetString("run")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	optSlice, _ := cmd.Flags().GetStringSlice("opt")
	opts := parseTestOpts(optSlice)

	if platform == "all" {
		return runTestAll(language, units, runFilter, verbose, format, opts)
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

	totalTests, totalFail, allResults := runOnPlatform(cmd.Context(), plat, runner, lang, units, runFilter, opts)

	return reportResults(allResults, totalTests, totalFail, format, verbose)
}

func runTestAll(language string, units []unit, runFilter string, verbose bool, format string, opts *ir.StructLit) error {
	names := codegen.Platforms()
	var ran, skipped, failedPlats int
	var grandTests, grandFail int
	var allResults []*codegen.TestResult

	for _, name := range names {
		plat := codegen.LookupPlatform(name)
		runner, _ := plat.(codegen.TestRunner)
		if runner == nil {
			// Provisional: a lang lookup failure is handled below, this only
			// skips the platform when neither test path exists.
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
		tests, fails, results := runOnPlatform(context.Background(), plat, runner, lang, units, runFilter, opts)
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

// A platform with no supported langs (`none`) is fine, and returns nil.
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

// A parse or check failure counts as a test failure: `./...` already skips
// `testdata/` and `.`/`_`-prefixed directories, so a walked file is expected
// to be valid SNGL.
//
// A unit is a package, so a test function reads the whole package it was
// written in: `sngl test` used to read every file alone, and a test could not
// name a component declared in the file next to it.
func runOnPlatform(ctx context.Context, plat codegen.PlatformGenerator, runner codegen.TestRunner, lang codegen.LangTranslator, units []unit, runFilter string, opts *ir.StructLit) (int, int, []*codegen.TestResult) {
	if ctx == nil {
		ctx = context.Background()
	}
	var totalTests, totalFail int
	var allResults []*codegen.TestResult

	for _, u := range units {
		filename := u.headline()
		absFilename, _ := filepath.Abs(filename)
		start := time.Now()
		doc, err := u.doc()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", u.name, err)
			totalFail++
			continue
		}
		slog.Info("parse", "unit", u.name, "duration", time.Since(start))

		if len(doc.TestFuncs()) == 0 {
			continue
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
		// A test run is a build for the platform under test, so the check has
		// to be configured for that target and not for whatever the source's
		// `output` block happens to name: a platform absent from the block
		// would otherwise never have its platform package merged, and every
		// stdlib component it renders through an override would report itself
		// unimplemented.
		pkg, err := checkDoc(doc, u.dir, true, build.SelectedTargets(langIdent(lang), plat.PlatformIdentifier())...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", u.name, err)
			totalFail++
			continue
		}
		slog.Info("check", "unit", u.name, "duration", time.Since(start))

		// Record the source path so headless runners (e.g. `none`) can
		// resolve sibling `<fixture>.snapshots/<name>.sngl` goldens.
		pkg.SourcePath = absFilename

		// runner.RunTests is the fallback for a pair with no launcher (`none`).
		var results []*codegen.TestResult
		if resolveLauncher(plat, lang) != nil {
			results, err = runViaLauncher(ctx, plat, lang, pkg, opts, filepath.Dir(filename), filepath.Base(filename))
		} else {
			results, err = safeRunTests(runner, pkg, lang, opts)
		}
		if err != nil {
			// The matrix runs every platform over every file, so a target
			// supporting fewer components would otherwise turn every program
			// using one of them red. Skip, naming the component.
			if missing, ok := errors.AsType[*codegen.UnimplementedComponent](err); ok {
				allResults = append(allResults, &codegen.TestResult{
					Component: filepath.Base(filename),
					Skipped:   true,
					SkipReason: fmt.Sprintf("%s does not implement %s",
						missing.Platform, missing.Component),
				})
				continue
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}

		for _, r := range results {
			allResults = append(allResults, r)
			totalTests += countTests(r)
			if !r.Passed && !r.Skipped {
				totalFail++
			}
		}
	}

	return totalTests, totalFail, allResults
}

// A panic becomes an error so one buggy fixture on one platform does not take
// down a --platform=all matrix.
func safeRunTests(runner codegen.TestRunner, pkg *ir.Package, lang codegen.LangTranslator, opts *ir.StructLit) (results []*codegen.TestResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in test runner: %v", r)
		}
	}()
	return runner.RunTests(pkg, lang, opts)
}

func reportResults(allResults []*codegen.TestResult, totalTests, totalFail int, format string, verbose bool) error {
	if format == "json" {
		return printJSON(allResults, totalTests, totalFail)
	}

	for _, r := range allResults {
		printResult(r, r.Component, verbose)
	}

	var totalSkip int
	for _, r := range allResults {
		totalSkip += countSkipped(r)
	}
	skipSuffix := ""
	if totalSkip > 0 {
		skipSuffix = fmt.Sprintf(", %d skipped", totalSkip)
	}

	fmt.Println()
	if totalFail > 0 {
		fmt.Printf("FAIL\n")
		fmt.Printf("not ok   %d tests, %d failures%s\n", totalTests, totalFail, skipSuffix)
		return fmt.Errorf("test failed")
	}
	// A run where nothing executed must not print PASS: the txtar fixtures
	// and internal/testutil match on that marker, so a skip that says PASS
	// makes every assertion after it vacuous.
	if totalTests == 0 && totalSkip > 0 {
		fmt.Printf("SKIP\n")
		fmt.Printf("no tests ran, %d skipped\n", totalSkip)
		return nil
	}
	fmt.Printf("PASS\n")
	fmt.Printf("ok   %d tests, 0 failures%s\n", totalTests, skipSuffix)
	return nil
}

func printResult(r *codegen.TestResult, prefix string, verbose bool) {
	name := prefix + "/" + r.Desc

	if verbose {
		fmt.Printf("=== RUN   %s\n", name)
	}

	if r.Skipped {
		fmt.Printf("--- SKIP: %s: %s\n", name, r.SkipReason)
	} else if r.Passed {
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
	if r.Skipped {
		return 0
	}
	n := 1
	for _, child := range r.Children {
		n += countTests(child)
	}
	return n
}

func countSkipped(r *codegen.TestResult) int {
	if r.Skipped {
		return 1
	}
	n := 0
	for _, child := range r.Children {
		n += countSkipped(child)
	}
	return n
}

type jsonOutput struct {
	Passed   bool         `json:"passed"`
	Tests    int          `json:"tests"`
	Failures int          `json:"failures"`
	Skipped  int          `json:"skipped,omitempty"`
	Results  []jsonResult `json:"results"`
}

type jsonResult struct {
	Name       string       `json:"name"`
	Passed     bool         `json:"passed"`
	Skipped    bool         `json:"skipped,omitempty"`
	SkipReason string       `json:"skip_reason,omitempty"`
	Error      string       `json:"error,omitempty"`
	Log        []string     `json:"log,omitempty"`
	Duration   float64      `json:"duration_s"`
	Children   []jsonResult `json:"children,omitempty"`
}

func printJSON(results []*codegen.TestResult, totalTests, totalFail int) error {
	out := jsonOutput{
		Passed:   totalFail == 0,
		Tests:    totalTests,
		Failures: totalFail,
	}
	for _, r := range results {
		out.Skipped += countSkipped(r)
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
		Name:       name,
		Passed:     r.Passed,
		Skipped:    r.Skipped,
		SkipReason: r.SkipReason,
		Error:      r.Error,
		Log:        r.Log,
		Duration:   r.Duration.Seconds(),
	}
	for _, child := range r.Children {
		jr.Children = append(jr.Children, toJSON(child, name))
	}
	return jr
}
