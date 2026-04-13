package javascript

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func scope() *codegen.ExprScope {
	return &codegen.ExprScope{
		ModelFields:    map[string]bool{"count": true},
		ComputedFields: map[string]bool{"doubled": true},
		LocalVars:      map[string]bool{"item": true},
		Renames:        map[string]string{"item": "item_0"},
	}
}

func TestTranslateExpr(t *testing.T) {
	s := scope()
	tests := []struct {
		name string
		node ast.Expr
		want string
	}{
		{"int literal", &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "42"}, "42"},
		{"float literal", &ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: "3.14"}, "3.14"},
		{"string literal", &ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: "hello"}, `"hello"`},
		{"bool true", &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "true"}, "true"},
		{"bool false", &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "false"}, "false"},
		{"null", &ast.LiteralExpr{Kind: ast.LiteralNull, Raw: "null"}, "null"},
		{"model field", &ast.IdentExpr{Name: "count"}, "state.count"},
		{"computed field", &ast.IdentExpr{Name: "doubled"}, "$doubled()"},
		{"local var with rename", &ast.IdentExpr{Name: "item"}, "item_0"},
		{"binary add", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
			Op:    ast.BinAdd,
			Right: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
		}, "(1 + 2)"},
		{"int division", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "7"},
			Op:    ast.BinDiv,
			Right: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
		}, "Math.trunc(7 / 2)"},
		{"unary not", &ast.UnaryExpr{
			Op:      ast.UnaryNot,
			Operand: &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "true"},
		}, "!true"},
		{"unary negate", &ast.UnaryExpr{
			Op:      ast.UnaryNeg,
			Operand: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "5"},
		}, "-5"},
		{"ternary", &ast.TernaryExpr{
			Cond: &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "true"},
			Then: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
			Else: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
		}, "(true ? 1 : 2)"},
		{"select", &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Field:   "value",
		}, "state.count.value"},
		{"index", &ast.IndexExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Index:   &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "0"},
		}, "state.count[0]"},
		{"element ref", &ast.ElementRefExpr{Name: "myBtn"},
			`document.querySelector('[data-sngl-id="myBtn"]')`},
		{"list", &ast.ListExpr{
			Elements: []ast.Expr{
				&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
				&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
			},
		}, "[1, 2]"},
		{"struct", &ast.StructExpr{
			Name: "Point",
			Fields: []ast.StructFieldLit{
				{Name: "x", Value: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"}},
				{Name: "y", Value: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"}},
			},
		}, "{x: 1, y: 2}"},
		{"nil expr", nil, "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateExpr(tt.node, s)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranslateCall(t *testing.T) {
	s := scope()
	s.NeededHelpers = map[string]bool{}
	tests := []struct {
		name string
		fn   string
		args []ast.Expr
		want string
	}{
		{"string()", "string", []ast.Expr{&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "42"}}, "String(42)"},
		{"int()", "int", []ast.Expr{&ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: "3.14"}}, "Math.trunc(3.14)"},
		{"float()", "float", []ast.Expr{&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "42"}}, "parseFloat(42)"},
		{"custom()", "myFunc", []ast.Expr{
			&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
			&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
		}, "myFunc(1, 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var argList []ast.ArgOrEventHandler
			for _, a := range tt.args {
				argList = append(argList, ast.Arg{Value: a})
			}
			call := &ast.CallExpr{
				Func: &ast.IdentExpr{Name: tt.fn},
				Args: ast.ArgList{Args: argList},
			}
			got := translateCall(call, s)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranslateMutation(t *testing.T) {
	s := scope()
	tests := []struct {
		name string
		node ast.Stmt
		want string
	}{
		{"assign", &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "count"},
			Op:     ast.AssignSet,
			Value:  &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "0"},
		}, "state.count = 0"},
		{"toggle", &ast.ToggleStmt{
			Target: &ast.IdentExpr{Name: "count"},
		}, "state.count = !state.count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts := translateMutation(tt.node, s)
			if len(stmts) != 1 {
				t.Fatalf("got %d stmts, want 1", len(stmts))
			}
			if stmts[0] != tt.want {
				t.Errorf("got %q, want %q", stmts[0], tt.want)
			}
		})
	}
}

func TestTranslateInterpolation(t *testing.T) {
	s := scope()
	node := &ast.InterpolationExpr{
		Parts: []ast.Expr{
			&ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: "count is "},
			&ast.IdentExpr{Name: "count"},
		},
	}
	got := translateExpr(node, s)
	want := "`count is ${state.count}`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
