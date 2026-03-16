package checker

import (
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
		err = Check(doc, "../../testdata", DefaultResolver())
		testutil.AssertErrors(t, err, checkErrs)
	})
}
