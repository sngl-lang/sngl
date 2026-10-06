package testrunner_test

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestRunFixtures(t *testing.T) {
	// Registered because a timer needs its platform: `sngl:time`'s `timer` has
	// no body, and the schedule is sngl:platform/none's override of it.
	langs, plats := testtargets.Targets()
	// Every fixture, `SKIP(codegen)` ones included: that directive opts a
	// fixture out of the platforms' code generators, and the interpreter is
	// not one -- it is the reference a fixture written ahead of its lowering
	// is run against.
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) {
			doc, err := parser.Parse(s.Filename, []byte(s.Source))
			if err != nil {
				// A fixture that does not parse declares nothing to run, and
				// its ERROR(parse) directive is the parser harness's to match.
				if len(s.PhaseErrors("parse")) > 0 {
					t.Skip("parse-phase fixture")
				}
				t.Fatalf("parse: %v", err)
			}
			pkg, diags := checker.Check(doc, &checker.Config{
				FS: s.FS, Dir: s.Dir, IsMain: true,
				Languages: langs, Platforms: plats,
				Targets: []ir.StaticTarget{{Platform: "none"}},
			})
			// What makes a fixture runnable is that it declares a test
			// function, which is readable from the fixture itself. A prefix
			// list stood here instead, and a fixture left off it had its
			// assertions read by nobody -- the platform harnesses only compile
			// one, and `sngl test` is a thing a person runs by hand. Six
			// effect_* fixtures went that way once, and
			// type_params_on_components another.
			if why := notRunnable(s, pkg); why != "" {
				t.Skip(why)
			}
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
	src := `import . "sngl:ui"
import . "sngl:test"
import ui "sngl:ui"
component counter node {
	var n = 0
	button #b(text="+", @click { n += 1 })
	text #o(value=string(n))
}
component main node {
	counter #l()
	counter #r()
}
ui.window {
	main
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

// notRunnable says why a fixture's assertions are not this harness's to
// evaluate, or "" when they are. Both halves are read off the fixture itself,
// which is the point: the prefix list this replaces was the one classifier in
// the repo that could not be read from the file it classified, and a family
// left off it went unevaluated in silence.
//
// A fixture is runnable when it declares a test function -- there is nothing
// to evaluate otherwise -- and its program is one that reaches the run at all.
// A deliberately-broken fixture is not: its ERROR directives name a phase
// before the run, and the harness for that phase is what matches them. The
// exception is a fixture that asserts a *runtime* failure, which is a fixture
// written to be run; it may carry check errors alongside, and those are
// tolerated below so the ERROR(test) directives can be matched at all.
func notRunnable(s testutil.Sample, pkg *ir.Package) string {
	fns, _, _ := codegen.CollectTestFuncs(pkg)
	if len(fns) == 0 {
		return "declares no test function"
	}
	if len(s.Errors) > 0 && !s.ExpectsError("test") {
		return "asserts a failure before the run"
	}
	return ""
}
