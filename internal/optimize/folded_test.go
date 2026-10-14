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
	src := "import ui \"sngl:ui\"\nimport time \"sngl:time\"\n\nui.window {\n\tui.text(value=\"hi\")\n}\n"
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

	var fn *ir.Func
	for _, imp := range pkg.Imports {
		if imp.Pkg != nil && imp.Pkg.Path == "sngl:time" {
			for _, f := range imp.Pkg.Funcs {
				if len(f.Block) > 0 {
					fn = f
					break
				}
			}
		}
	}
	if fn == nil {
		t.Fatal("sngl:time declares no bodied func")
	}
	plant := func() *ir.Return {
		ret := &ir.Return{Value: &ir.Binary{Op: ast.BinAdd, Type: ir.TypInt,
			Left:  &ir.Literal{Type: ir.TypInt, Value: "1"},
			Right: &ir.Literal{Type: ir.TypInt, Value: "2"}}}
		fn.Block = []ir.Stmt{ret}
		return ret
	}

	plant()
	if err := Optimize(pkg, cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := fn.Block[0].(*ir.Return).Value.(*ir.Binary); !ok {
		t.Errorf("the second Optimize folded %s again", fn.Name)
	}

	plant()
	if err := Optimize(pkg, &Config{Platform: "html", Language: "js"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fn.Block[0].(*ir.Return).Value.(*ir.Binary); ok {
		t.Errorf("a fresh Config left %s unfolded", fn.Name)
	}
}
