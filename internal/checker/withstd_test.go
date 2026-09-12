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
	if strings.Contains(src, "sngl:") {
		return src
	}
	// No sngl:ui/draw. It is a specialised surface a program pays for by
	// importing it, and a target answers for the shapes only when one does --
	// so dot-importing it into every fixture asked each stub platform in these
	// tests to implement seven shapes for a program that draws nothing. A
	// fixture that draws writes its own imports, which this helper leaves
	// alone.
	return "import . \"sngl:ui\"\nimport . \"sngl:time\"\nimport . \"sngl:dialog\"\nimport . \"sngl:macro\"\nimport \"sngl:i18n\"\n" + src
}
