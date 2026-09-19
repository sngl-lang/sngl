// Package fixtures runs every testdata/*.sngl fixture through every phase the
// fixture asks for, once.
//
// # Why one runner
//
// Each phase used to own its fixtures. internal/parser globbed testdata and
// parsed it four times over four tests; internal/checker globbed and checked
// it three times; internal/optimize and internal/lspcore each globbed it
// again. Nine walks over one directory, and the work they shared -- reading
// the file, reading its directives, parsing it, checking it -- was redone in
// every one.
//
// Two of those walks were near-duplicates that had drifted rather than
// diverged on purpose. TestCheckTestdata asserted every ERROR(check) and
// ERROR(lint) directive; TestCheckProjectTestdata checked the same 553
// fixtures with the same config and then only *logged* what came back, a
// comment explaining that the directives were a v1 spelling the v2 checker
// might not match. It cost 8.8 seconds to log.
//
// So a fixture is read once, its directives read once, parsed once and checked
// once, and each phase is handed what the phase before it produced. What each
// phase asserts is unchanged -- this is the same set of claims, made in one
// pass.
//
// # What decides which phases run
//
// The fixture does, through its own directives, and nothing here is a list of
// fixture names:
//
//   - ERROR(parse) — the parse must fail that way, and nothing downstream runs:
//     there is no tree to format, check or fold.
//   - NOFMT — the fixture is exempt from being written the way `sngl fmt`
//     writes it, and the directive is held to that claim.
//   - ERROR(check) / ERROR(lint) — the diagnostic must appear on that line.
//   - FOLD — the optimizer must fold that line's initializer to that value.
//   - a `lsp_` name — the //@ markers are asserted against lspcore.
//
// # Coverage
//
// This package is why `go tool verify` instruments the whole library for the
// root test package alone. One binary reaching the parser, the checker, the
// optimizer and the LSP over 553 programs is the broadest coverage the repo
// has; asking for it from all 65 test binaries, which is what -coverpkg=./...
// did, instrumented every package into every one of them to get it.
package fixtures

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testtargets"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Run walks testdata/*.sngl, one subtest per fixture, and then the SNGL
// embedded in the documentation.
func Run(t *testing.T) {
	for s := range testutil.TestdataSamples(t) {
		t.Run(s.Name, func(t *testing.T) { runOne(t, s) })
	}
	// A doc sample is a fixture of another origin: extracted from prose rather
	// than written as a file, so it carries no directives and there is nothing
	// to assert about it but that formatting leaves it alone. It is walked here
	// because the alternative is a second walk in another package for two
	// assertions.
	t.Run("docs", func(t *testing.T) {
		for s := range testutil.DocSamples(t) {
			t.Run(s.Name, func(t *testing.T) {
				assertLiteralsSurviveFormat(t, s.Filename, s.Source)
				assertCommentsSurviveFormat(t, s.Filename, s.Source)
			})
		}
	})
}

func runOne(t *testing.T, s testutil.Sample) {
	doc, err := parser.Parse(s.Filename, []byte(s.Source))

	if !assertParse(t, s, err) {
		// A fixture asserting a parse error has no tree, so every phase below
		// would be asserting against error recovery rather than against the
		// program. That was a t.Skip in each of the old walks.
		return
	}

	assertFormatted(t, s, doc)
	assertLiteralsSurviveFormat(t, s.Filename, s.Source)
	assertCommentsSurviveFormat(t, s.Filename, s.Source)
	assertFormatIsIdempotent(t, s, doc)

	pkg := check(t, s, doc)
	assertNoSilentDyn(t, s, pkg)
	assertFolds(t, s, doc)

	if strings.HasPrefix(s.Name, "lsp_") {
		assertMarkers(t, s)
	}
}

// assertParse holds a fixture to its ERROR(parse) directives, and reports
// whether there is a tree to carry on with.
//
// The directive used to be a skip in every testdata consumer, so a fixture
// asserting a parse message asserted nothing -- the message could change, or
// the error move to another line, and no test noticed.
func assertParse(t *testing.T, s testutil.Sample, err error) bool {
	expected := s.PhaseErrors("parse")
	if len(expected) == 0 {
		if err != nil {
			t.Errorf("parse failed: %v", err)
			return false
		}
		return true
	}
	if err == nil {
		t.Errorf("expected a parse error, got none")
		return false
	}
	// The errors arrive joined into one message, one per line, each prefixed
	// "file:line:col: ".
	lines := strings.Split(err.Error(), "\n")
	for _, exp := range expected {
		prefix := fmt.Sprintf("%s:%d:", s.Filename, exp.Pos())
		found := false
		for _, got := range lines {
			if strings.HasPrefix(got, prefix) && strings.Contains(got, exp.Substring) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("line %d: expected a parse error containing %q, got:\n%v", exp.Pos(), exp.Substring, err)
		}
	}
	return false
}

// assertFormatted holds every fixture to being written the way `sngl fmt`
// writes it, so a formatting change has to be looked at rather than discovered
// later as drift. A fixture whose exact layout is the thing under test opts
// out with `// NOFMT "reason"`, and the directive is held to that claim in
// turn.
func assertFormatted(t *testing.T, s testutil.Sample, doc *ast.Document) {
	got := parser.Format(doc)
	switch {
	case s.NoFmt && got == s.Source:
		t.Errorf("NOFMT is stale (%s): the fixture is formatted, so drop the directive", s.NoFmtReason)
	case !s.NoFmt && got != s.Source:
		t.Errorf("fixture is not formatted; run `sngl fmt testdata`:\n%s", firstDiff(s.Source, got))
	}
}

// Formatting twice must be formatting once.
func assertFormatIsIdempotent(t *testing.T, s testutil.Sample, doc *ast.Document) {
	once := parser.Format(doc)
	again, err := parser.Parse(s.Filename, []byte(once))
	if err != nil {
		t.Errorf("formatted output does not re-parse: %v", err)
		return
	}
	if twice := parser.Format(again); twice != once {
		t.Errorf("second format differs from the first:\n%s", firstDiff(once, twice))
	}
}

// check runs the checker against every registered target and holds the result
// to the fixture's ERROR(check) and ERROR(lint) directives.
func check(t *testing.T, s testutil.Sample, doc *ast.Document) *ir.Package {
	langs, plats := testtargets.Targets()
	cfg := &checker.Config{
		IsMain:          true,
		FS:              s.FS,
		Dir:             s.Dir,
		Languages:       langs,
		Platforms:       plats,
		TargetsComplete: true,
	}
	// A fixture that imports a directory package resolves through the stub in
	// resolver.go rather than off disk.
	for _, st := range doc.Stmts {
		if _, ok := st.(*ast.Import); ok {
			cfg.Resolver = newTestResolver()
			break
		}
	}
	pkg, diags := checker.Check(doc, cfg)

	expected := s.PhaseErrors("check")
	if len(expected) == 0 {
		for _, d := range diags {
			if d.Severity == ir.Error {
				t.Errorf("unexpected error: %s", d.Error())
			}
		}
	}
	for _, exp := range expected {
		found := false
		for _, d := range diags {
			if d.Severity != ir.Error {
				continue
			}
			// exp.Pos(), not exp.Line: a directive may name the position it
			// expects when a comment cannot be written there. Reading the
			// comment's own line ignored that and passed a fixture naming a
			// line the file does not have.
			if d.Pos.Line == exp.Pos() && strings.Contains(d.Msg, exp.Substring) {
				found = true
				break
			}
		}
		if !found {
			var got strings.Builder
			for _, d := range diags {
				fmt.Fprintf(&got, "\n  %s", d.Error())
			}
			t.Errorf("line %d: expected error containing %q, got:%s", exp.Pos(), exp.Substring, got.String())
		}
	}
	testutil.AssertDiagnostics(t, diags, s.Errors, "lint")
	return pkg
}

// assertFolds holds each FOLD directive to the value the optimizer reaches.
//
// It checks the fixture a second time rather than reusing check's package: the
// optimizer rewrites the IR in place, and every phase after this one would
// then be reading a folded program rather than a checked one. The old walk
// checked with a Config naming no targets, which is why this one does too.
func assertFolds(t *testing.T, s testutil.Sample, doc *ast.Document) {
	if len(s.Folds) == 0 || s.ExpectsError("check") {
		return
	}
	pkg, diags := checker.Check(doc, &checker.Config{FS: s.FS, Dir: s.Dir, IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := optimize.Optimize(pkg, &optimize.Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	byLine := collectVarsByLine(pkg)
	for _, fd := range s.Folds {
		v, ok := byLine[fd.Line]
		if !ok {
			t.Errorf("line %d: no var found at FOLD directive line", fd.Line)
			continue
		}
		lit, ok := v.Init.(*ir.Literal)
		if !ok {
			t.Errorf("line %d (%s): expected folded literal, got %T (%s)", fd.Line, v.Name, v.Init, irExprDescr(v.Init))
			continue
		}
		if got := litValue(lit); got != fd.Expected {
			t.Errorf("line %d (%s): got %v (%T), want %v (%T)", fd.Line, v.Name, got, got, fd.Expected, fd.Expected)
		}
	}
}
