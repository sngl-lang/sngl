package checker_test

import (
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

// What a program adds to a go: declaration -- a method, an override -- is
// the program's, as it is for a declaration of a SNGL package it imports.
func TestProgramAdditionsLeaveANativeImport(t *testing.T) {
	entry := &ir.StructDef{Name: "Entry", Foreign: ir.Foreign{Path: "api", Name: "api.Entry"}}
	entry.Fields = []*ir.StructField{{Name: "N", Type: intType()}}
	square := &ir.Func{
		Name:    "Square",
		Params:  []*ir.Param{{Name: "x", Type: intType()}},
		Return:  intType(),
		Foreign: ir.Foreign{Path: "api", Name: "api.Square"},
	}
	native := map[string]*ir.NativeImport{"go:api": {ImportPath: "api", Structs: []*ir.StructDef{entry}, Funcs: []*ir.Func{square}}}
	doc, err := parser.Parse("test.sngl", []byte(`import . "go:api"
import "sngl:platform/html"

func Entry.twice(e Entry) int {
    return e.N * 2
}

func Square[html.platform] {
    return x
}

var e = Entry{N=2}
var n = e.twice() + Square(1)
`))
	if err != nil {
		t.Fatal(err)
	}
	langs, plats := testtargets.Targets()
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Resolver: &importResolver{native: native}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if len(entry.Methods) > 0 {
		t.Errorf("the program's method was written onto the imported struct: %v", entry.Methods)
	}
	if pkg.Symbols.Root.Added[entry]["twice"] == nil {
		t.Errorf("the program holds no method on the imported struct")
	}
	if len(square.PlatformOverrides) > 0 {
		t.Errorf("the program's override was written onto the imported func: %v", square.PlatformOverrides)
	}
	if pkg.Overlay.Overrides[square] == nil {
		t.Errorf("the program holds no override of the imported func")
	}
}
