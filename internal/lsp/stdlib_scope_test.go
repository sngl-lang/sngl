package lsp

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// Hover resolves against the checked package's symbol table, so it must show
// standard-library declarations only to a file that imported them. Before the
// library became an explicit import there was nothing to distinguish.
func TestStdlibHoverFollowsImports(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"imported", "import . \"sngl:ui\"\n\ncomponent main {\n    text(value=\"x\")\n}\n", true},
		{"not imported", "component main {\n    var n int = 1\n}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.Parse("t.sngl", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
			_, got := lookupStdlibSymbol(pkg, "text")
			if got != tc.want {
				t.Errorf("hover for stdlib %q: got %v, want %v", "text", got, tc.want)
			}
		})
	}
}
