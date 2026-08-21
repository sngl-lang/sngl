package testrunner_test

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestRunFixtures(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		if !strings.HasPrefix(s.Name, "test_") && !strings.HasPrefix(s.Name, "component_") {
			continue
		}
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{FS: s.FS, Dir: s.Dir, IsMain: true})
			// Fixtures with `ERROR(check)` directives intentionally contain
			// type errors; run the interpreter anyway so test-phase error
			// directives can be matched against the runtime diagnostics.
			checkDirs := s.PhaseErrors("check")
			for _, d := range diags {
				if d.Severity != ir.Error {
					continue
				}
				if !hasMatchingErrorDirective(d.Error(), checkDirs) {
					t.Fatalf("check: %s", d.Error())
				}
			}

			testDirs := s.PhaseErrors("test")

			// Thread the on-disk source path so t.snapshot() can resolve
			// sibling <fixture>.snapshots/<name>.sngl goldens.
			pkg.SourcePath = filepath.Join(s.Dir, s.Filename)

			results, err := testrunner.Run(pkg)
			if err != nil {
				t.Fatalf("run: %v", err)
			}

			for _, r := range results {
				checkResult(t, r, testDirs)
			}
		})
	}
}

func hasMatchingErrorDirective(msg string, dirs []testutil.ErrorDirective) bool {
	for _, d := range dirs {
		if strings.Contains(msg, d.Substring) {
			return true
		}
	}
	return false
}

func TestChildrenLiveAcrossMutations(t *testing.T) {
	src := `import . "sngl://std"
component counter {
	var n = 0
	button #b(text="+", @click { n += 1 })
	text #o(value=string(n))
}
component main {
	counter #l()
	counter #r()
}
func testIsolation(t Test, c main) {
	var l = c.l
	var r = c.r
	t.assert(l.o.value == "0")
	l.b.click()
	t.assert(l.o.value == "1")
	t.assert(r.o.value == "0")
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	results, err := testrunner.Run(pkg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no test results")
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("test failed: %s\nerror: %s", r.Desc, r.Error)
		}
	}
}

func checkResult(t *testing.T, r *codegen.TestResult, dirs []testutil.ErrorDirective) {
	t.Helper()

	// Match each recorded failure against a directive at its line (or the
	// line directly above for standalone-comment placement). A precise
	// line-tied directive that doesn't substring-match is reported as a
	// targeted mismatch — easier to debug than a file-wide fallthrough.
	unmatched := make([]codegen.TestFailure, 0)
	for _, f := range r.Failures {
		matched := false
		anyAtLine := false
		if f.Line > 0 {
			for _, d := range dirs {
				if d.Line == f.Line || d.Line == f.Line-1 {
					anyAtLine = true
					if strings.Contains(f.Message, d.Substring) {
						matched = true
						break
					}
				}
			}
		}
		if matched {
			continue
		}
		if anyAtLine {
			desc := r.Desc
			if r.Component != "" {
				desc = r.Component + ": " + desc
			}
			t.Errorf("test %q: failure at line %d not matched by directive at that line: %s", desc, f.Line, f.Message)
			continue
		}
		unmatched = append(unmatched, f)
	}

	// Legacy fallback: any unmatched failure is OK if some directive in
	// the file substring-matches it. Keeps fixtures with func-line
	// directives (test_errors.sngl, test_error_runtime.sngl, …) green.
	for _, f := range unmatched {
		matched := false
		for _, d := range dirs {
			if strings.Contains(f.Message, d.Substring) {
				matched = true
				break
			}
		}
		if !matched {
			desc := r.Desc
			if r.Component != "" {
				desc = r.Component + ": " + desc
			}
			t.Errorf("test %q failed: %s", desc, f.Message)
		}
	}

	if !r.Passed && len(r.Failures) == 0 {
		desc := r.Desc
		if r.Component != "" {
			desc = r.Component + ": " + desc
		}
		t.Errorf("test %q did not pass but had no failures recorded", desc)
	}

	for _, child := range r.Children {
		checkResult(t, child, dirs)
	}
}
