package checker

import (
	"io/fs"
	"maps"
	"slices"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// Storing a package's new version drops the old one and every kept package
// checked against it, however indirectly, and nothing else.
func TestStoringAnEditEjectsItsImporters(t *testing.T) {
	l := newLibCache()
	geom := &pkgEntry{key: "geom"}
	shapes := &pkgEntry{key: "shapes", deps: []*pkgEntry{geom}}
	scene := &pkgEntry{key: "scene", deps: []*pkgEntry{shapes}}
	colors := &pkgEntry{key: "colors"}
	for _, e := range []*pkgEntry{geom, shapes, scene, colors} {
		l.storeEntry(e.key, e)
	}
	l.storeEntry("geom", &pkgEntry{key: "geom"})
	if got := slices.Sorted(maps.Keys(l.pkgEntries)); !slices.Equal(got, []string{"colors", "geom"}) {
		t.Errorf("kept %v after geom was edited, want colors and the new geom", got)
	}
	if l.pkgEntries["geom"] == geom {
		t.Error("the old geom is still kept")
	}

	l.storeEntry("shapes", &pkgEntry{key: "shapes", deps: []*pkgEntry{l.pkgEntries["geom"]}})
	l.storeEntry("geom", nil)
	if got := slices.Sorted(maps.Keys(l.pkgEntries)); !slices.Equal(got, []string{"colors"}) {
		t.Errorf("kept %v after geom could no longer be kept, want colors", got)
	}
}

type dirsResolver map[string]string

func (r dirsResolver) Resolve(_ fs.FS, path string) ([]*ast.Document, error) {
	doc, err := parser.Parse(path+".sngl", []byte(r[path]))
	if err != nil {
		return nil, err
	}
	return []*ast.Document{doc}, nil
}

func (dirsResolver) ResolveScheme(string, string, string) (*ir.NativeImport, error) {
	return nil, nil
}

func (dirsResolver) ResolveSchemeFS(string, string, string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

// A kept package reports what the library loads it reached reported, though
// the build that kept it had them reported already through another import.
func TestKeptPackageReportsItsLibraryLoads(t *testing.T) {
	lib, err := parser.Parse("broken.sngl", []byte("const x int = \"s\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	libs := newLibCache()
	resolver := dirsResolver{"a": "import broken \"sngl:broken\"\n\nconst y = broken.x\n"}
	check := func(src string) []ir.Diagnostic {
		doc, err := parser.Parse("main.sngl", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		_, diags := Check(doc, &Config{IsMain: true, Resolver: resolver, LibSources: map[string][]*ast.Document{"broken": {lib}}, libs: libs})
		return diags
	}
	if diags := check("import broken \"sngl:broken\"\nimport a \"./a\"\n"); len(diags) == 0 {
		t.Fatal("the library reported nothing to the build that loaded it")
	}
	if len(libs.pkgEntries) == 0 {
		t.Fatal("a was not kept")
	}
	if diags := check("import a \"./a\"\n"); len(diags) == 0 {
		t.Error("a build reaching the library only through a kept package was not told what it reported")
	}
}
