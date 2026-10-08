package codegen

import (
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
)

// The #[gen.native] mark is read off a target's source before anything is
// checked, so it has to resolve sngl:x/gen's alias the way the checker does:
// unwritten, the alias is the path's last segment; a dot import writes no
// qualifier; and a file that does not import the package has no such mark.
func TestNativeMarkFollowsTheImportAlias(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"default alias", "import \"sngl:x/gen\"\nimport build \"sngl:build\"\n\n#[gen.native(\"k\")]\ncomponent platform() build.platform\n", "k"},
		{"written alias", "import g \"sngl:x/gen\"\nimport build \"sngl:build\"\n\n#[g.native(\"k\")]\ncomponent platform() build.platform\n", "k"},
		{"dot import", "import . \"sngl:x/gen\"\nimport build \"sngl:build\"\n\n#[native(\"k\")]\ncomponent platform() build.platform\n", "k"},
		{"another package's native", "import g \"sngl:x/gen\"\nimport gen \"sngl:x/other\"\nimport build \"sngl:build\"\n\n#[gen.native(\"k\")]\ncomponent platform() build.platform\n", ""},
		{"not imported", "import build \"sngl:build\"\n\n#[gen.native(\"k\")]\ncomponent platform() build.platform\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.Parse("t.sngl", []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := nativeIn([]*ast.Document{doc}); got != tc.want {
				t.Errorf("nativeIn = %q, want %q", got, tc.want)
			}
		})
	}
}
