package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestPurity_PureExpressionFunc(t *testing.T) {
	// func add(a int, b int) => a + b
	fn := &ast.FuncDef{
		Name: "add",
		Body: ast.Expr{SNGL: &ast.BinaryExpr{
			Op:    ast.BinAdd,
			Left:  &ast.IdentExpr{Name: "a"},
			Right: &ast.IdentExpr{Name: "b"},
		}},
	}
	dataNames := map[string]bool{"count": true}
	p := analyzePurity(fn, dataNames)
	if p != ast.PurityPure {
		t.Errorf("expected PurityPure, got %d", p)
	}
}

func TestPurity_ReadonlyExpressionFunc(t *testing.T) {
	// func label() => "Count: " + string(count)
	fn := &ast.FuncDef{
		Name: "label",
		Body: ast.Expr{SNGL: &ast.IdentExpr{Name: "count"}},
	}
	dataNames := map[string]bool{"count": true}
	p := analyzePurity(fn, dataNames)
	if p != ast.PurityReadonly {
		t.Errorf("expected PurityReadonly, got %d", p)
	}
}

func TestPurity_MutatingBlockFunc(t *testing.T) {
	// func reset() { count = 0 }
	fn := &ast.FuncDef{
		Name: "reset",
		Block: &ast.FuncBlock{
			Stmts: []ast.Node{
				&ast.AssignStmt{
					Target: &ast.IdentExpr{Name: "count"},
					Op:     ast.AssignSet,
					Value:  &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
				},
			},
		},
	}
	dataNames := map[string]bool{"count": true}
	p := analyzePurity(fn, dataNames)
	if p != ast.PurityMutates {
		t.Errorf("expected PurityMutates, got %d", p)
	}
}

func TestPurity_ReadonlyBlockFunc(t *testing.T) {
	// func doubled() { return count * 2 }
	fn := &ast.FuncDef{
		Name: "doubled",
		Block: &ast.FuncBlock{
			Return: &ast.BinaryExpr{
				Op:    ast.BinMul,
				Left:  &ast.IdentExpr{Name: "count"},
				Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			},
		},
	}
	dataNames := map[string]bool{"count": true}
	p := analyzePurity(fn, dataNames)
	if p != ast.PurityReadonly {
		t.Errorf("expected PurityReadonly, got %d", p)
	}
}

func TestPurity_PureBlockFunc(t *testing.T) {
	// func double(x int) { return x * 2 }
	fn := &ast.FuncDef{
		Name: "double",
		Block: &ast.FuncBlock{
			Return: &ast.BinaryExpr{
				Op:    ast.BinMul,
				Left:  &ast.IdentExpr{Name: "x"},
				Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			},
		},
	}
	dataNames := map[string]bool{"count": true}
	p := analyzePurity(fn, dataNames)
	if p != ast.PurityPure {
		t.Errorf("expected PurityPure, got %d", p)
	}
}

func TestPurity_EmitIsMutating(t *testing.T) {
	fn := &ast.FuncDef{
		Name: "notify",
		Block: &ast.FuncBlock{
			Stmts: []ast.Node{
				&ast.EmitStmt{Name: "change"},
			},
		},
	}
	p := analyzePurity(fn, nil)
	if p != ast.PurityMutates {
		t.Errorf("expected PurityMutates, got %d", p)
	}
}

func TestPurity_ToggleIsMutating(t *testing.T) {
	fn := &ast.FuncDef{
		Name: "toggle",
		Block: &ast.FuncBlock{
			Stmts: []ast.Node{
				&ast.ToggleStmt{Target: &ast.IdentExpr{Name: "visible"}},
			},
		},
	}
	p := analyzePurity(fn, map[string]bool{"visible": true})
	if p != ast.PurityMutates {
		t.Errorf("expected PurityMutates, got %d", p)
	}
}

func TestPurity_StdlibIsUnknown(t *testing.T) {
	fn := &ast.FuncDef{
		Name:     "stdlib_fn",
		IsStdlib: true,
	}
	p := analyzePurity(fn, nil)
	if p != ast.PurityUnknown {
		t.Errorf("expected PurityUnknown, got %d", p)
	}
}
