package testharness

import (
	"testing"

	"duckfam.us/sngl/ast"
)

func TestGroup_byComponent(t *testing.T) {
	mk := func(name, comp string) *ast.FuncDef {
		fn := &ast.FuncDef{Name: name}
		fn.Params.Params = []ast.Param{
			{Name: "t", Type: &ast.NamedType{Name: "Test"}},
			{Name: "c", Type: &ast.NamedType{Name: comp}},
		}
		return fn
	}
	standalone := func(name string) *ast.FuncDef {
		fn := &ast.FuncDef{Name: name}
		fn.Params.Params = []ast.Param{{Name: "t", Type: &ast.NamedType{Name: "Test"}}}
		return fn
	}

	groups := Group([]*ast.FuncDef{
		mk("testA", "Counter"),
		mk("testB", "Counter"),
		mk("testC", "Form"),
		standalone("testD"),
	})

	got := map[string]int{}
	for _, g := range groups {
		got[g.Component] = len(g.Funcs)
	}
	if got["Counter"] != 2 || got["Form"] != 1 || got[""] != 1 {
		t.Fatalf("unexpected grouping: %+v", got)
	}
}
