package optimize

import "strings"

// withStdSrc prepends the stdlib dot import to an inline test source. These
// tests exercise codegen and analysis, not import resolution, so the import is injected rather
// than repeated in every literal; testdata/*.sngl carry it for real.
func withStdSrc(src string) string {
	if strings.Contains(src, "sngl:") {
		return src
	}
	return "import . \"sngl:ui\"\nimport . \"sngl:app\"\nimport . \"sngl:dialog\"\nimport . \"sngl:macro\"\nimport . \"sngl:ui/draw\"\n" + src
}
