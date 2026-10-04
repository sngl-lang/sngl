package codegen_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// A target whose package is a lib/ directory and that registers no Go is a
// target all the same: the registry finds it by its layout, describes it from
// its build node's doc comment, and refuses to generate for it. `none` is the
// first, and nothing in Go names it.
func TestDeclaredTargetsComeFromLib(t *testing.T) {
	for _, tc := range []struct {
		uri    string
		target any
	}{
		{"platform/none", codegen.LookupPlatform("none")},
		{"language/none", codegen.LookupLang("none")},
	} {
		switch tg := tc.target.(type) {
		case *codegen.DeclaredPlatform:
			if tg.Description() == "" {
				t.Errorf("%s: no description", tc.uri)
			}
			if err := tg.Generate(&codegen.Request{}, nil); err == nil {
				t.Errorf("%s: Generate succeeded", tc.uri)
			}
		case *codegen.DeclaredLang:
			if tg.Description() == "" {
				t.Errorf("%s: no description", tc.uri)
			}
		default:
			t.Fatalf("%s: registered as %T, want a declared target", tc.uri, tc.target)
		}
		docs := checker.PackageSource(tc.uri)
		if len(docs) == 0 {
			t.Fatalf("%s: no source", tc.uri)
		}
		for _, d := range docs {
			if f := fileOf(d); !strings.HasPrefix(f, tc.uri+"/") {
				t.Errorf("%s: file %s is not served from lib/%s", tc.uri, f, tc.uri)
			}
		}
	}
}

func fileOf(d *ast.Document) string {
	if len(d.Stmts) == 0 || d.Stmts[0].StmtPos() == nil {
		return ""
	}
	return d.Stmts[0].StmtPos().File
}
