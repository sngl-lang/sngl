package sngl_test

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/goldentest"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// TestProvidedDocsSurviveBuilds holds the compiler to what sharing a target's
// parsed source across checks depends on: nothing after the parser writes to
// an AST. checker.ProvidedDocs hands every check the same documents, so a
// write made while building one program would be read back by the next.
//
// Every golden fixture is built first, through every target it declares --
// check, optimize, lower and codegen, since IR keeps its AST and any phase
// could reach one -- and then each target's shared documents are compared
// against a fresh parse of the same bytes.
func TestProvidedDocsSurviveBuilds(t *testing.T) {
	goldentest.Run(t, "testdata/*.txtar", false, false)

	var targets []any
	var prefixes []string
	for _, name := range codegen.Platforms() {
		targets = append(targets, codegen.LookupPlatform(name))
		prefixes = append(prefixes, "platform/"+codegen.LookupPlatform(name).PlatformIdentifier())
	}
	for _, name := range codegen.Langs() {
		targets = append(targets, codegen.LookupLang(name))
		prefixes = append(prefixes, "language/"+codegen.LookupLang(name).LanguageIdentifier())
	}
	compared := 0
	for i, target := range targets {
		p, ok := target.(interface{ PackageFS() fs.FS })
		if !ok || p.PackageFS() == nil {
			continue
		}
		shared := map[string]any{}
		for _, doc := range checker.ProvidedDocs(target) {
			shared[docFile(doc)] = doc
		}
		fsys := p.PackageFS()
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			data, err := fs.ReadFile(fsys, e.Name())
			if err != nil {
				t.Fatal(err)
			}
			name := prefixes[i] + "/" + e.Name()
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
		t.Fatal("no target served a package, so nothing was compared")
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
