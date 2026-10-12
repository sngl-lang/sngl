package checker_test

import (
	"slices"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// A directory package and a scheme's are checked once for every check of the
// same source, and an edit is checked again with every package that imported
// what it edited.
func TestImportedPackagesAreKept(t *testing.T) {
	r := &schemeFSResolver{
		pkgs: map[string]string{
			"shapes":      "import geom \"./geom\"\n\nfunc origin() => geom.Point{x=0}\n",
			"shapes/geom": "struct Point {\n    x int\n}\n",
		},
		fsPkgs: map[string]string{"t:colors": "const red = \"#f00\"\n"},
	}
	check := func() (shapes, colors *ir.Package, diags []ir.Diagnostic) {
		t.Helper()
		doc, err := parser.Parse("main.sngl", []byte("import shapes \"./shapes\"\nimport colors \"t:colors\"\n\nconst p = shapes.origin()\nconst c = colors.red\n"))
		if err != nil {
			t.Fatal(err)
		}
		pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Resolver: r, Dir: t.Name()})
		for _, imp := range pkg.Imports {
			switch imp.Path {
			case "./shapes":
				shapes = imp.Pkg
			case "t:colors":
				colors = imp.Pkg
			}
		}
		return shapes, colors, diags
	}

	shapes, colors, diags := check()
	if len(diags) > 0 {
		t.Fatalf("first check: %s", diags[0].Error())
	}
	shapesAgain, colorsAgain, _ := check()
	if shapesAgain != shapes {
		t.Error("a second check of the same directory package checked it again")
	}
	if colorsAgain != colors {
		t.Error("a second check of the same scheme package checked it again")
	}

	r.pkgs["shapes/geom"] = "struct Point {\n    y int\n}\n"
	r.fsPkgs["t:colors"] = "const red = \"#e00\"\n"
	shapesEdited, colorsEdited, diags := check()
	if shapesEdited == shapes {
		t.Error("shapes was not checked again after the package it imports was edited")
	}
	if colorsEdited == colors {
		t.Error("an edited scheme package was not checked again")
	}
	unknownX := func(d ir.Diagnostic) bool { return d.Msg == `unknown field "x" on struct Point` }
	if !slices.ContainsFunc(diags, unknownX) {
		t.Errorf("shapes was not held to the edited geom: %v", diags)
	}
	// What reported something is not kept: there would be nothing to report
	// it again from.
	if _, _, diags := check(); !slices.ContainsFunc(diags, unknownX) {
		t.Errorf("a second check of a package with an error did not report it: %v", diags)
	}
}
