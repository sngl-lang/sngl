//go:build !js

package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// TestOptimize_GoImportConstFoldsOnKotlin confirms a pure go:// call used as a
// const folds to a literal at build time even when the target language is
// kotlin (which cannot call go:// at runtime). The folded value is all the
// target needs — no surviving call — so this must keep working regardless of
// any scheme/target runtime-support guard.
func TestOptimize_GoImportConstFoldsOnKotlin(t *testing.T) {
	dir := projectDir()
	if dir == "" {
		t.Skip("could not find project root")
	}

	// const x = purepkg.Double(21)
	callAST := &ast.CallExpr{Func: &ast.SelectExpr{
		Operand: &ast.IdentExpr{Name: "purepkg"},
		Field:   "Double",
	}}
	c := &ir.Var{
		Name:    "x",
		Type:    ir.TypInt,
		IsConst: true,
		Init: &ir.Call{AST: callAST, Type: ir.TypInt, Args: []ir.CallArg{
			{Value: &ir.Literal{AST: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "21"}, Type: ir.TypInt, Raw: "21"}},
		}},
	}
	pkg := &ir.Package{
		Consts: []*ir.Var{c},
		Imports: []*ir.Import{{
			Alias: "purepkg",
			Native: &ir.NativeImport{
				ImportPath: "git.duckfam.us/jonathan/sngl/internal/optimize/testdata/purepkg",
				Funcs: []*ir.Func{{
					Name:    "Double",
					Foreign: ir.Foreign{Name: "purepkg.Double", Path: "purepkg"},
					Purity:  ir.PurityPure,
					Params:  []*ir.Param{{Name: "x", Type: ir.TypInt}},
					Return:  ir.TypInt,
				}},
			},
		}},
	}

	if err := Optimize(pkg, &Config{Platform: "android", Language: "kotlin", Dir: dir}); err != nil {
		t.Fatalf("kotlin build must not fail on a foldable go:// const: %v", err)
	}
	lit, ok := c.Init.(*ir.Literal)
	if !ok {
		t.Fatalf("const did not fold to a literal on kotlin target: got %T", c.Init)
	}
	if lit.Raw != "42" {
		t.Errorf("expected folded value 42, got %q", lit.Raw)
	}
}
