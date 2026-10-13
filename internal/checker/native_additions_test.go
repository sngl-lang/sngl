package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

func nativeAPI() (*ir.StructDef, *ir.Func, map[string]*ir.NativeImport) {
	entry := &ir.StructDef{Name: "Entry", Foreign: ir.Foreign{Path: "api", Name: "api.Entry"}}
	entry.Fields = []*ir.StructField{{Name: "N", Type: intType()}}
	square := &ir.Func{
		Name:    "Square",
		Params:  []*ir.Param{{Name: "x", Type: intType()}},
		Return:  intType(),
		Foreign: ir.Foreign{Path: "api", Name: "api.Square"},
	}
	return entry, square, map[string]*ir.NativeImport{"go:api": {ImportPath: "api", Structs: []*ir.StructDef{entry}, Funcs: []*ir.Func{square}}}
}

func checkNative(t *testing.T, src string, native map[string]*ir.NativeImport) (*ir.Package, []ir.Diagnostic) {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	langs, plats := testtargets.Targets()
	return checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats, Resolver: &importResolver{native: native}})
}

// What a program adds to a go: declaration is the program's, as it is for a
// declaration of a SNGL package it imports.
func TestProgramAdditionsLeaveANativeImport(t *testing.T) {
	entry, _, native := nativeAPI()
	pkg, diags := checkNative(t, `import . "go:api"

func Entry.twice(e Entry) int {
    return e.N * 2
}

var e = Entry{N=2}
var n = e.twice()
`, native)
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
}

// No target emits a go: function, so a body overriding one would never run.
func TestNativeFuncOverrideRefused(t *testing.T) {
	_, square, native := nativeAPI()
	_, diags := checkNative(t, `import . "go:api"
import "sngl:platform/html"

func Square[html.platform] {
    return x
}

var n = Square(1)
`, native)
	var refused bool
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, `"Square" is declared outside SNGL`) {
			refused = true
		}
	}
	if !refused {
		t.Errorf("an override of a go: function was accepted: %v", diags)
	}
	if len(square.PlatformOverrides) > 0 {
		t.Errorf("the refused override was written onto the imported func: %v", square.PlatformOverrides)
	}
}
