package interp_test

import (
	"testing"
	"testing/fstest"

	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// TestPrepareSharesWhatTheProgramDoesNotReach holds the interpreter's copy to
// the library its program reaches: a component it never names is the checked
// one, and one it renders is its own.
func TestPrepareSharesWhatTheProgramDoesNotReach(t *testing.T) {
	fsys := fstest.MapFS{"main.sngl": &fstest.MapFile{Data: []byte(`import ui "sngl:ui"

ui.window {
    ui.text(value="hi")
}
`)}}
	doc, err := build.ParsePackageFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := build.Check(doc, build.CheckConfig{FS: fsys, IsMain: true})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := interp.Prepare(checked)
	if err != nil {
		t.Fatal(err)
	}
	ui := func(pkg *ir.Package) map[string]*ir.Component {
		out := map[string]*ir.Component{}
		for _, imp := range pkg.Imports {
			if imp.Pkg != nil && imp.Pkg.Path == "sngl:ui" {
				for _, c := range imp.Pkg.Components {
					out[c.Name] = c
				}
			}
		}
		return out
	}
	was, now := ui(checked), ui(prepared)
	if now["text"] == was["text"] {
		t.Error("ui.text, which the program renders, was not copied")
	}
	if now["tabs"] != was["tabs"] {
		t.Error("ui.tabs, which the program never names, was copied")
	}
}
