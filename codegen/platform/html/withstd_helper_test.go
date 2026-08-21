package html

import "strings"

// withStdSrc prepends the library dot imports to an inline test source. These
// tests exercise codegen, not import resolution, so the imports are injected
// rather than repeated in every literal; testdata/*.sngl carry them for real.
// draw is included because the canvas tests here build shape trees.
func withStdSrc(src string) string {
	if strings.Contains(src, "sngl://") {
		return src
	}
	return "import . \"sngl://std\"\nimport . \"sngl://draw\"\n" + src
}
