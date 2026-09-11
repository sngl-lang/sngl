package checker

import "strings"

// withStd prepends the stdlib dot import to an inline test source. See the
// identical helper in the checker_test package for why it is injected.
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
