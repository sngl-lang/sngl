package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

// An override an import wrote is that import's to answer for: a program
// reaching it, once or through two paths, reports nothing more about it.
func TestImportedOverridePlacementReportedOnce(t *testing.T) {
	r := &mockResolver{pkgs: map[string]string{
		"a": "import gen \"sngl:x/gen\"\nimport go \"sngl:language/go\"\nimport ui \"sngl:ui\"\n\ncomponent[go.language] ui.button {\n    gen.emit(file=\"a.txt\")\n}\n",
		"b": "import a \"a\"\n",
	}}
	langs, plats := testtargets.Targets()
	for _, src := range []string{"import a \"a\"\n", "import a \"a\"\nimport b \"b\"\n"} {
		doc, err := parser.Parse("main.sngl", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		_, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Resolver: r})
		var n int
		for _, d := range diags {
			if d.Severity == ir.Error && strings.Contains(d.Msg, "gen.emit is written as the whole body") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%q: the misplaced gen.emit was reported %d times, want once: %v", src, n, diags)
		}
	}
}
