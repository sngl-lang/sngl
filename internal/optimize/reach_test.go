package optimize_test

import (
	"fmt"
	"testing"
	"testing/fstest"

	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/ir"
)

// TestUnreachedImportsAreLeftAlone holds the first optimize to the library
// declarations the program reaches: of two components alike in every way but
// that the program renders one, only that one has its loop head folded, its
// population read into a list, and its html override swapped in.
func TestUnreachedImportsAreLeftAlone(t *testing.T) {
	widget := func(name string) []byte {
		return fmt.Appendf(nil, `import ui "sngl:ui"
import "sngl:platform/html"

const tags%[1]s = ["x", "y"]

component %[1]s() ui.node {
    for var t = tags%[1]s {
        ui.text(value=t)
    }
    ui.select {
        ui.choice(label="a")
    }
}

component %[1]sOv() ui.node {
    ui.text(value="base")
}

component %[1]sOv[html.platform] {
    ui.text(value="html")
}
`, name)
	}
	fsys := fstest.MapFS{
		"main.sngl": &fstest.MapFile{Data: []byte(`import ui "sngl:ui"
import "lib"

ui.window {
    lib.Used()
    lib.UsedOv()
}
`)},
		"lib/used.sngl":   &fstest.MapFile{Data: widget("Used")},
		"lib/unused.sngl": &fstest.MapFile{Data: widget("Unused")},
	}
	doc, err := build.ParsePackageFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := build.Check(doc, build.CheckConfig{FS: fsys, IsMain: true})
	if err != nil {
		t.Fatal(err)
	}
	pkg := ir.ClonePackageFor(checked, "html", "js")
	if err := optimize.Optimize(pkg, &optimize.Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatal(err)
	}

	comps := map[string]*ir.Component{}
	for _, imp := range pkg.Imports {
		if imp.Pkg != nil && imp.Alias == "lib" {
			for _, c := range imp.Pkg.Components {
				comps[c.Name] = c
			}
		}
	}
	for _, name := range []string{"Used", "Unused"} {
		c := comps[name]
		if c == nil {
			t.Fatalf("lib declares no %s", name)
		}
		reached := name == "Used"
		if got := comps[name+"Ov"].SpecializedFor != ""; got != reached {
			t.Errorf("%sOv: override swapped in = %v, want %v", name, got, reached)
		}
		var loop *ir.For
		var sel *ir.NodeInst
		_ = ir.Walk(c.Stmts, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.For:
				loop = x
			case *ir.NodeInst:
				if x.Component != nil && x.Component.Name == "select" {
					sel = x
				}
			}
			return nil
		})
		if loop == nil || sel == nil {
			t.Fatalf("%s lost its loop or its select", name)
		}
		if _, folded := loop.Iter.(*ir.ListLit); folded != reached {
			t.Errorf("%s: loop head folded = %v, want %v", name, folded, reached)
		}
		listed := false
		for _, p := range sel.Props {
			listed = listed || p.Name == "options"
		}
		if listed != reached {
			t.Errorf("%s: population read = %v, want %v", name, listed, reached)
		}
	}
}

// TestSpecializationReadsTheCopysRecord holds specialization to what
// ClonePackageFor recorded as overridden: a declaration left off the record
// keeps its declared body, though it carries an override for the target.
func TestSpecializationReadsTheCopysRecord(t *testing.T) {
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
	pkg := ir.ClonePackageFor(checked, "html", "js")
	var window, text *ir.Component
	kept := pkg.Overridden[:0:0]
	for _, sym := range pkg.Overridden {
		c, ok := sym.(*ir.Component)
		switch {
		case ok && c.Name == "window":
			window = c
			continue
		case ok && c.Name == "text":
			text = c
		}
		kept = append(kept, sym)
	}
	if window == nil || text == nil {
		t.Fatalf("the copy recorded no html override of ui.window and ui.text: %v", pkg.Overridden)
	}
	pkg.Overridden = kept

	ir.SpecializeForTarget(pkg, "html", "js")
	if window.SpecializedFor != "" {
		t.Error("ui.window, left off the record, was specialized")
	}
	if text.SpecializedFor == "" {
		t.Error("ui.text, on the record, was not specialized")
	}
}
