package sngl_test

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/goldentest"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/lib"
)

// TestProvidedDocsSurviveBuilds holds the compiler to what sharing a target's
// parsed source across checks depends on: nothing after the parser writes to
// an AST. A target's package is parsed once per process (checker.PackageSource)
// and handed to every check, so a write made while building one program would
// be read back by the next.
//
// Every golden fixture is built first, through every target it declares --
// check, optimize, lower and codegen, since IR keeps its AST and any phase
// could reach one -- and then each target's shared documents are compared
// against a fresh parse of the same bytes.
func TestProvidedDocsSurviveBuilds(t *testing.T) {
	goldentest.Run(t, "testdata/*.txtar", false, false)

	compared := 0
	for _, pkg := range lib.Packages() {
		if !strings.HasPrefix(pkg, "platform/") && !strings.HasPrefix(pkg, "language/") {
			continue
		}
		shared := map[string]any{}
		for _, doc := range checker.PackageSource(pkg) {
			shared[docFile(doc)] = doc
		}
		entries, err := fs.ReadDir(lib.FS, pkg)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			name := pkg + "/" + e.Name()
			data, err := fs.ReadFile(lib.FS, name)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := parser.Parse(name, data)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := shared[name]
			if !ok {
				t.Errorf("%s: no shared document", name)
				continue
			}
			if !reflect.DeepEqual(got, fresh) {
				t.Errorf("%s: a build wrote to the shared AST; it no longer matches a fresh parse of the same source", name)
			}
			compared++
		}
	}
	if compared == 0 {
		t.Fatal("no target package found under lib/, so nothing was compared")
	}
}

// docFile is the file a document was parsed from, off its first positioned
// statement.
func docFile(doc *ast.Document) string {
	for _, stmt := range doc.Stmts {
		if p := stmt.StmtPos(); p != nil && p.File != "" {
			return p.File
		}
	}
	return ""
}
