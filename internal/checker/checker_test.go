package checker

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestFixtures(t *testing.T) {
	testutil.RunFixtures(t, "../../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		parseErrs := testutil.Filter(dirs, "parse")
		if len(parseErrs) > 0 {
			return // skip files that test parser errors
		}
		checkErrs := testutil.Filter(dirs, "check")
		doc, err := testutil.ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		err = Check(doc, "../../testdata", DefaultResolver(), true)
		// When check error directives exist, also merge CheckTests
		// diagnostics so ERROR(check) directives on test blocks match.
		if len(checkErrs) > 0 {
			diags := CheckTests(doc)
			if len(diags) > 0 {
				var msgs []string
				if err != nil {
					msgs = append(msgs, err.Error())
				}
				for _, d := range diags {
					if d.Pos.Line > 0 {
						msgs = append(msgs, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
					} else {
						msgs = append(msgs, d.Msg)
					}
				}
				err = fmt.Errorf("%s", strings.Join(msgs, "\n"))
			}
		}
		testutil.AssertErrors(t, err, checkErrs)
	})
}

func TestCheckTests(t *testing.T) {
	testutil.RunFixtures(t, "../../testdata", func(t *testing.T, path string, dirs []testutil.ErrorDirective) {
		base := strings.TrimSuffix(path, ".sngl")
		isTestFile := strings.Contains(base, "test_")
		isCheckTestError := strings.HasSuffix(base, "error_unknown_test_component")
		if !isTestFile && !isCheckTestError {
			return
		}
		// Skip test files with runtime error directives — those intentionally
		// reference undefined vars which CheckTests may also flag.
		if isTestFile && len(testutil.Filter(dirs, "test")) > 0 {
			return
		}
		doc, err := testutil.ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		diags := CheckTests(doc)

		checkErrs := testutil.Filter(dirs, "check")
		if len(checkErrs) == 0 {
			if len(diags) > 0 {
				t.Errorf("expected no diagnostics, got %v", diags)
			}
			return
		}
		var msgs []string
		for _, d := range diags {
			if d.Pos.Line > 0 {
				msgs = append(msgs, fmt.Sprintf("%d:%d: %s", d.Pos.Line, d.Pos.Column, d.Msg))
			} else {
				msgs = append(msgs, d.Msg)
			}
		}
		var diagErr error
		if len(msgs) > 0 {
			diagErr = fmt.Errorf("%s", strings.Join(msgs, "\n"))
		}
		testutil.AssertErrors(t, diagErr, checkErrs)
	})
}
