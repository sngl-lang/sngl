package bubbletea_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/bubbletea"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestGeneratedTests(t *testing.T) {
	matches, err := filepath.Glob("../../../testdata/test_*.sngl")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no test_*.sngl files found")
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			// Skip files with ERROR(test) directives — those are runtime error tests
			dirs, _ := testutil.ParseDirectives(path)
			for _, d := range dirs {
				if d.Phase == "test" {
					t.Skip("file has ERROR(test) directives")
				}
			}

			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			doc, err := parser.Parse(path, src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			cfg := bubbletea.Config{Package: "generated"}

			// Generate test file
			testSrc, err := bubbletea.CompileTests(doc, cfg)
			if err != nil {
				t.Fatalf("CompileTests: %v", err)
			}
			if testSrc == nil {
				t.Skip("no compilable tests")
			}

			// Get expected results from interpreter
			interpResults, err := testrunner.Run(doc)
			if err != nil {
				t.Fatalf("interpreter run: %v", err)
			}

			// Write test file to temp dir and run go test
			dir := t.TempDir()

			// Write go.mod
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module generated\ngo 1.23\n"), 0644)

			// Write test file
			os.WriteFile(filepath.Join(dir, "model_test.go"), testSrc, 0644)

			// Run go test -v -json to get structured output
			cmd := exec.Command("go", "test", "-v", "-count=1", "-vet=off", "./...")
			cmd.Dir = dir
			out, runErr := cmd.CombinedOutput()

			// Parse go test output for pass/fail counts
			goStatus := parseGoTestOutput(string(out))
			goPassed := 0
			goFailed := 0
			for _, passed := range goStatus {
				if passed {
					goPassed++
				} else {
					goFailed++
				}
			}

			// Count interpreter results (excluding skipped tests)
			interpPassed := 0
			interpFailed := 0
			for _, r := range interpResults {
				if shouldSkipTest(r, doc) {
					continue
				}
				countResults(r, &interpPassed, &interpFailed)
			}

			if len(goStatus) == 0 && runErr != nil {
				t.Logf("go test output:\n%s", out)
				t.Fatalf("no generated tests compiled: %v", runErr)
			}

			if goFailed > 0 {
				t.Logf("go test output:\n%s", out)
				t.Fatalf("generated tests had %d failures (expected 0)", goFailed)
			}

			t.Logf("generated: %d passed, interpreter (compilable): %d passed", goPassed, interpPassed)
		})
	}
}

func shouldSkipTest(r *codegen.TestResult, doc *ast.Document) bool {
	// Find the corresponding test function
	for _, fn := range doc.TestFuncs() {
		if fn.Name == r.Desc {
			return bubbletea.ShouldSkipTestFunc(fn)
		}
	}
	return false
}

func countResults(r *codegen.TestResult, passed, failed *int) {
	if r.Passed {
		*passed++
	} else {
		*failed++
	}
	for _, child := range r.Children {
		countResults(child, passed, failed)
	}
}

// parseGoTestOutput extracts pass/fail status from go test -v output.
func parseGoTestOutput(output string) map[string]bool {
	status := map[string]bool{}
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- PASS:") {
			name := extractTestName(line)
			if name != "" {
				status[name] = true
			}
		} else if strings.HasPrefix(line, "--- FAIL:") {
			name := extractTestName(line)
			if name != "" {
				status[name] = false
			}
		}
	}
	return status
}

func extractTestName(line string) string {
	// "--- PASS: TestCounter_Increments/multiple_increments (0.00s)"
	parts := strings.Fields(line)
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}
