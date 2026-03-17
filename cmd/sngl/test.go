package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/testrunner"
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
	testCmd.Flags().Bool("verbose", false, "verbose output")
	testCmd.Flags().String("format", "text", "output format: text or json")
}

func runTest(cmd *cobra.Command, args []string) error {
	files, err := discoverFiles(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .sngl files found")
	}

	runFilter, _ := cmd.Flags().GetString("run")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")

	var totalTests, totalFail int
	var allResults []*testrunner.Result

	for _, filename := range files {
		doc, err := parseTestFile(filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}

		if len(doc.Tests) == 0 {
			continue
		}

		// Merge sibling .sngl files for component definitions
		doc, err = mergeDir(doc, filename)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}

		// Check
		if doc.App != nil || len(doc.Tests) > 0 {
			if err := checker.Check(doc, filepath.Dir(filename), checker.DefaultResolver()); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
				totalFail++
				continue
			}
		}

		diags := checker.CheckTests(doc)
		if len(diags) > 0 {
			for _, d := range diags {
				fmt.Fprintf(os.Stderr, "%s: %s\n", filename, d.Msg)
			}
			totalFail++
			continue
		}

		// Run
		results, err := testrunner.Run(doc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filename, err)
			totalFail++
			continue
		}

		for _, r := range results {
			if runFilter != "" && !strings.Contains(r.Desc, runFilter) {
				continue
			}
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

func printResult(r *testrunner.Result, prefix string, verbose bool) {
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

	for _, child := range r.Children {
		printResult(child, name, verbose)
	}
}

func countTests(r *testrunner.Result) int {
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
	Duration float64      `json:"duration_s"`
	Children []jsonResult `json:"children,omitempty"`
}

func printJSON(results []*testrunner.Result, totalTests, totalFail int) error {
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

func toJSON(r *testrunner.Result, prefix string) jsonResult {
	name := prefix + "/" + r.Desc
	jr := jsonResult{
		Name:     name,
		Passed:   r.Passed,
		Error:    r.Error,
		Duration: r.Duration.Seconds(),
	}
	for _, child := range r.Children {
		jr.Children = append(jr.Children, toJSON(child, name))
	}
	return jr
}

func parseTestFile(filename string) (*ast.Document, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseSNGL(filename, f)
}

// mergeDir merges component definitions from sibling .sngl files in the same
// directory so tests can reference components defined in other files.
func mergeDir(doc *ast.Document, filename string) (*ast.Document, error) {
	dir := filepath.Dir(filename)
	base := filepath.Base(filename)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return doc, nil
	}

	for _, e := range entries {
		if e.IsDir() || !isSNGLFile(e.Name()) || e.Name() == base {
			continue
		}
		path := filepath.Join(dir, e.Name())
		sibling, err := parseTestFile(path)
		if err != nil {
			continue // skip unparseable sibling files
		}
		doc.Components = append(doc.Components, sibling.Components...)
		doc.Structs = append(doc.Structs, sibling.Structs...)
		doc.Enums = append(doc.Enums, sibling.Enums...)
		// If the test doc has no Data/Computeds/Consts but the sibling has main,
		// pull those in too.
		if sibling.App != nil && doc.App == nil {
			doc.App = sibling.App
			doc.Data = append(doc.Data, sibling.Data...)
			doc.Computeds = append(doc.Computeds, sibling.Computeds...)
			doc.Consts = append(doc.Consts, sibling.Consts...)
		}
	}

	return doc, nil
}
