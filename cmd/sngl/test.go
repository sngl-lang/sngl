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

	plat := codegen.LookupPlatform(platform)
	if plat == nil {
		return fmt.Errorf("unknown platform: %s", platform)
	}
	runner, ok := plat.(codegen.TestRunner)
	if !ok {
		return fmt.Errorf("platform %q does not support testing", platform)
	}

	var lang codegen.LangTranslator
	if language != "" {
		lang = codegen.LookupLang(language)
		if lang == nil {
			return fmt.Errorf("unknown language: %s", language)
		}
	}

	explicitFiles := explicitFileSet(args)

	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	runFilter, _ := cmd.Flags().GetString("run")
	verbose, _ := cmd.Flags().GetBool("verbose") // root persistent flag
	format, _ := cmd.Flags().GetString("format")

	var totalTests, totalFail int
	var allResults []*codegen.TestResult

	for _, filename := range files {
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

		absFilename, _ := filepath.Abs(filename)
		if !explicitFiles[absFilename] {
			start = time.Now()
			doc = mergeDir(doc, filename)
			slog.Info("merge", "file", filename, "duration", time.Since(start))
		}

		if len(doc.TestFuncs()) > 0 {
			start = time.Now()
			if _, err := checkDoc(doc, filepath.Dir(filename), true); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
				totalFail++
				continue
			}
			slog.Info("check", "file", filename, "duration", time.Since(start))
		}

		// Filter tests by --run pattern
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

		results, err := runner.RunTests(doc, lang)
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
			fmt.Printf("    %s\n", r.Error)
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
