package build_test

import (
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

func checkKept(t *testing.T, fsys fstest.MapFS, main string) (*ir.Package, error) {
	t.Helper()
	doc, err := parser.Parse("main.sngl", []byte(main))
	if err != nil {
		t.Fatal(err)
	}
	fsys["main.sngl"] = &fstest.MapFile{Data: []byte(main)}
	return build.Check(doc, build.CheckConfig{FS: fsys, IsMain: true, Dir: t.Name()})
}

// A kept package brings the contexts of the packages it imported, though the
// build that kept it adopted them through another import.
func TestKeptPackageBringsItsImportsContexts(t *testing.T) {
	fsys := fstest.MapFS{
		"c/c.sngl": {Data: []byte("context #theme(\"light\")\n\nfunc current() => theme\n")},
		"a/a.sngl": {Data: []byte("import ui \"sngl:ui\"\nimport c \"../c\"\n\ncomponent bar ui.node {\n    ui.text(value=c.current())\n}\n")},
	}
	const window = "\noutput {\n    none {\n        html\n    }\n}\n\nui.window(title=\"t\") {\n    a.bar\n}\n"
	for _, main := range []string{
		"import ui \"sngl:ui\"\nimport c \"./c\"\nimport a \"./a\"\n" + window,
		"import ui \"sngl:ui\"\nimport a \"./a\"\n" + window,
	} {
		pkg, err := checkKept(t, fsys, main)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := build.Emit(pkg, build.Options{Name: "main.sngl", Dir: ".", ProjectFS: fsys}); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
}

// A directory this build checked afresh -- under another replace map, so
// another key -- is the one every kept package it reuses must have imported,
// or two versions of one type meet in the program.
func TestKeptPackageAgreesWithTheBuildsOwn(t *testing.T) {
	fsys := fstest.MapFS{
		"c/c.sngl": {Data: []byte("struct Point {\n    x int\n}\n")},
		"x/x.sngl": {Data: []byte("import c \"../c\"\nimport e \"e\" => \"../e\"\n\nfunc fx(p c.Point) => p.x + e.k\n")},
		"e/e.sngl": {Data: []byte("const k = 1\n")},
		"y/y.sngl": {Data: []byte("import c \"../c\"\n\nfunc fy(p c.Point) => p.x\n")},
	}
	if _, err := checkKept(t, fsys, "import c \"./c\"\nimport x \"./x\"\nimport y \"./y\"\n\nconst p = c.Point{x=1}\nconst a = x.fx(p)\nconst b = y.fy(p)\n"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	fsys["e/e.sngl"] = &fstest.MapFile{Data: []byte("const k = 2\n")}
	if _, err := checkKept(t, fsys, "import x \"./x\"\nimport y \"./y\"\nimport c \"./c\"\n\nconst p = c.Point{x=1}\nconst a = x.fx(p)\nconst b = y.fy(p)\n"); err != nil {
		t.Fatalf("second build: %v", err)
	}
}
