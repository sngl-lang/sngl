package checker_test

import "strings"

// withStd prepends the stdlib dot import to an inline test source.
//
// These tests exercise checker internals, not import resolution, so the
// import is injected rather than repeated in every literal. The requirement
// itself is covered where it is the subject: testdata/*.sngl carry the import
// as real programs, and cmd/sngl/testdata/import_dot.txt asserts that omitting
// it fails.
//
// A source that manages its own stdlib import is left alone.
func withStd(src string) string {
	if strings.Contains(src, "sngl://") {
		return src
	}
	return "import . \"sngl://std\"\n" + src
}
