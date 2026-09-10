package checker

import "strings"

// withStd prepends the stdlib dot import to an inline test source. See the
// identical helper in the checker_test package for why it is injected.
func withStd(src string) string {
	if strings.Contains(src, "sngl:") {
		return src
	}
	return "import . \"sngl:ui\"\nimport . \"sngl:time\"\nimport . \"sngl:dialog\"\nimport . \"sngl:macro\"\nimport . \"sngl:ui/draw\"\nimport \"sngl:i18n\"\n" + src
}
