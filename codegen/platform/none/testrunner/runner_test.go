package testrunner_test

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestRunFixtures(t *testing.T) {
	matches, err := filepath.Glob("../../../../testdata/test_*.sngl")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no test_*.sngl files found")
	}

	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			dirs, err := testutil.ParseDirectives(path)
			if err != nil {
				t.Fatalf("parse directives: %v", err)
			}

			doc, err := testutil.ParseFile(path)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			testDirs := testutil.Filter(dirs, "test")

			results, err := testrunner.Run(doc)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			for _, r := range results {
				checkResult(t, r, testDirs)
			}
		})
	}
}

func checkResult(t *testing.T, r *codegen.TestResult, dirs []testutil.ErrorDirective) {
	t.Helper()

	// Check if this result matches an expected-failure directive.
	for _, d := range dirs {
		if r.Error != "" && strings.Contains(r.Error, d.Substring) {
			return
		}
	}

	if !r.Passed {
		desc := r.Desc
		if r.Component != "" {
			desc = r.Component + ": " + desc
		}
		t.Errorf("test %q failed: %s", desc, r.Error)
	}

	for _, child := range r.Children {
		checkResult(t, child, dirs)
	}
}
