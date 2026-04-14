package testrunner_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestRunFixtures(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if !strings.HasPrefix(s.Name, "test_") {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			testDirs := s.PhaseErrors("test")

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
