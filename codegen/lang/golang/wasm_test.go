//go:build !js

package golang

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func TestGenerateGoWASMBridge(t *testing.T) {
	funcs := []codegen.WASMFunc{
		{Name: "Add", ParamTypes: []string{"int", "int"}, ReturnType: "int"},
		{Name: "Greet", ParamTypes: []string{"string"}, ReturnType: "string"},
		{Name: "Init", ParamTypes: []string{"pkg.Config"}, ReturnType: "", HasErrorReturn: true},
		{Name: "Search", ParamTypes: []string{"string", "[]string", "int"}, ReturnType: "[]string", HasErrorReturn: true},
		{Name: "List", ParamTypes: nil, ReturnType: "[]pkg.Result", HasErrorReturn: true},
		{Name: "Notify", ParamTypes: []string{""}, ReturnType: ""},
	}

	src := generateGoWASMBridge("example.com/some/pkg", funcs)

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "main.go", src, parser.AllErrors); err != nil {
		t.Fatalf("generated bridge does not parse:\n%s\n\nerror: %v", src, err)
	}

	mustContain := []string{
		`pkg "example.com/some/pkg"`,
		`"encoding/json"`,
		`"reflect"`,
		`var a0 pkg.Config`,
		`sngl_fromJS(args[0], &a0)`,
		`var a1 []string`,
		`sngl_fromJS(args[1], &a1)`,
		`pkg.Init(a0)`,
		`pkg.Search(a0, a1, a2)`,
		`return sngl_toJS(result)`,
		`if err != nil`,
		`a0 := args[0]`, // dyn passthrough for Notify
		`func sngl_fromJS(`,
		`func sngl_toJS(`,
		`func sngl_lowerFirst(`,
	}
	for _, want := range mustContain {
		if !strings.Contains(src, want) {
			t.Errorf("generated bridge missing %q\n\n%s", want, src)
		}
	}
}

func TestGenerateGoWASMBridge_PrimitivesOnly_NoMarshalHelpers(t *testing.T) {
	funcs := []codegen.WASMFunc{
		{Name: "Add", ParamTypes: []string{"int", "int"}, ReturnType: "int"},
		{Name: "Toggle", ParamTypes: []string{"bool"}, ReturnType: "bool"},
	}

	src := generateGoWASMBridge("example.com/p", funcs)

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "main.go", src, parser.AllErrors); err != nil {
		t.Fatalf("generated bridge does not parse:\n%s\n\nerror: %v", src, err)
	}

	for _, banned := range []string{`"encoding/json"`, `"reflect"`, `sngl_toJS`, `sngl_fromJS`} {
		if strings.Contains(src, banned) {
			t.Errorf("primitives-only bridge should not contain %q\n\n%s", banned, src)
		}
	}
}
