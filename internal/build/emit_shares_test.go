package build_test

import (
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

// emitLeavesImportsAlone builds main.sngl in fsys for the targets its output
// block names and reports every object of a package it imports that the build
// wrote: a build's copy shares what its program does not reach, and that is
// the checked IR every later build reads.
func emitLeavesImportsAlone(t *testing.T, fsys fstest.MapFS) {
	t.Helper()
	doc, err := build.ParsePackageFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := build.Check(doc, build.CheckConfig{FS: fsys, IsMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var imports []*ir.Package
	seen := map[*ir.Package]bool{checked: true}
	var walk func(p *ir.Package)
	walk = func(p *ir.Package) {
		for _, imp := range p.Imports {
			if imp.Pkg != nil && !seen[imp.Pkg] {
				seen[imp.Pkg] = true
				imports = append(imports, imp.Pkg)
				walk(imp.Pkg)
			}
		}
	}
	walk(checked)
	before := make([]testutil.Graph, len(imports))
	for i, p := range imports {
		before[i] = testutil.GraphOf(p)
	}
	if _, err := build.Emit(checked, build.Options{Name: "main.sngl", Dir: t.TempDir(), OutDir: t.TempDir(), ProjectFS: fsys}); err != nil {
		t.Fatal(err)
	}
	for i, p := range imports {
		if ch := before[i].Changed(testutil.GraphOf(p)); len(ch) > 0 {
			t.Errorf("the build wrote %q: %d objects, first %.300s", p.Path, len(ch), ch[0])
		}
	}
}

func TestEmitLeavesANamespaceReadsVarCopied(t *testing.T) {
	emitLeavesImportsAlone(t, fstest.MapFS{
		"main.sngl": &fstest.MapFile{Data: []byte(`import ui "sngl:ui"
import "lib"

output {
    go {
        bubbletea()
    }
}

ui.window {
    ui.button(text="n {lib.calls}", @click { lib.calls += 1 })
}
`)},
		"lib/lib.sngl": &fstest.MapFile{Data: []byte(`var calls = 1 + 1
`)},
	})
}
