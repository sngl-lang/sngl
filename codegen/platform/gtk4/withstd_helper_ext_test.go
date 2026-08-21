package gtk4_test

import "strings"

// withStdSrc prepends the stdlib dot import to an inline test source. See the
// identical helper in the gtk4 package for why it is injected.
func withStdSrc(src string) string {
	if strings.Contains(src, "sngl://") {
		return src
	}
	return "import . \"sngl://std\"\nimport . \"sngl://draw\"\n" + src
}
