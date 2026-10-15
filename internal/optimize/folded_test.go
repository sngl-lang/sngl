package optimize

import (
	"os"
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// The second Optimize of a build folds the program and leaves an imported
// package the first one folded as it stands; a fresh Config folds it again.
func TestImportedPackageIsFoldedOncePerConfig(t *testing.T) {
	src := "import ui \"sngl:ui\"\n\nui.window {\n\tui.text(value=\"hi\")\n}\n"
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	checked, diags := checker.Check(doc, &checker.Config{FS: os.DirFS("."), Dir: ".", IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatal(d.Error())
		}
	}
	pkg := ir.ClonePackage(checked)
	cfg := &Config{Platform: "html", Language: "js"}
	if err := Optimize(pkg, cfg); err != nil {
		t.Fatal(err)
	}

	var title *ir.Prop
	for _, imp := range pkg.Imports {
		if imp.Pkg != nil && imp.Pkg.Path == "sngl:ui" {
			for _, c := range imp.Pkg.Components {
				if c.Name == "window" {
					title = c.Props[0]
				}
			}
		}
	}
	if title == nil || title.Name != "title" {
		t.Fatal("sngl:ui declares no window title")
	}
	plant := func() {
		title.Default = &ir.Binary{Op: ast.BinAdd, Type: ir.TypString,
			Left:  &ir.Literal{Type: ir.TypString, Value: "a"},
			Right: &ir.Literal{Type: ir.TypString, Value: "b"}}
	}

	plant()
	if err := Optimize(pkg, cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := title.Default.(*ir.Binary); !ok {
		t.Error("the second Optimize folded the window title again")
	}

	plant()
	if err := Optimize(pkg, &Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := title.Default.(*ir.Binary); ok {
		t.Error("a fresh Config left the window title unfolded")
	}
}
