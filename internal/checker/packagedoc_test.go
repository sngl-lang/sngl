package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// A package comment is a run of line comments at the top of a file, separated
// from what follows by a blank line. The blank line is the whole rule: without
// it the run documents the declaration below, and both readings look identical
// in source.
func TestPackageDoc(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"blank line separates", "// Package prose.\n// More prose.\n\nimport . \"sngl:ui\"\n", "Package prose. More prose."},
		{"no blank line documents the decl", "// Doc for the struct.\nstruct Box { v int }\n", ""},
		{"second block after the run is not included", "// Package prose.\n\n// Note about the import.\nimport . \"sngl:ui\"\n", "Package prose."},
		{"file of only comments", "// Package prose.\n// More.\n", "Package prose. More."},
		{"no leading comment", "import . \"sngl:ui\"\n", ""},
		{"block comment is not a package comment", "/* nope */\n\nimport . \"sngl:ui\"\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parser.Parse("t.sngl", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := PackageDoc(doc); got != tc.want {
				t.Errorf("PackageDoc() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Every library package documents itself; the doc lookup reads these rather
// than hardcoding a blurb for one of them.
func TestLibraryPackagesHaveDocs(t *testing.T) {
	for _, pkg := range []string{"builtin", "ui", "ui/draw", "dialog", "test", "macro"} {
		var found string
		for _, d := range PackageDocsFor(pkg) {
			if s := PackageDoc(d); s != "" {
				found = s
				break
			}
		}
		if found == "" {
			t.Errorf("sngl:%s has no package comment", pkg)
		}
	}
}
