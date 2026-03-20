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
		node ast.Node
		want string
	}{
		{"int literal", &ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}, "42"},
		{"float literal", &ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}, "3.14"},
		{"string literal", &ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString}, `"hello"`},
		{"bool true", &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool}, "true"},
		{"bool false", &ast.LiteralExpr{Value: false, Kind: ast.LiteralBool}, "false"},
		{"null", &ast.LiteralExpr{Value: nil, Kind: ast.LiteralNull}, "null"},
		{"model field", &ast.IdentExpr{Name: "count"}, "state.count"},
		{"computed field", &ast.IdentExpr{Name: "doubled"}, "$doubled()"},
		{"local var with rename", &ast.IdentExpr{Name: "item"}, "item_0"},
		{"binary add", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Op:    ast.BinAdd,
			Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "(1 + 2)"},
		{"int division", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Value: 7, Kind: ast.LiteralInt},
			Op:    ast.BinDiv,
			Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "Math.trunc(7 / 2)"},
		{"unary not", &ast.UnaryExpr{
			Op:      ast.UnaryNot,
			Operand: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
		}, "!true"},
		{"unary negate", &ast.UnaryExpr{
			Op:      ast.UnaryNeg,
			Operand: &ast.LiteralExpr{Value: 5, Kind: ast.LiteralInt},
		}, "-5"},
		{"ternary", &ast.TernaryExpr{
			Cond: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
			Then: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Else: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "(true ? 1 : 2)"},
		{"select", &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Field:   "value",
		}, "state.count.value"},
		{"index", &ast.IndexExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Index:   &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
		}, "state.count[0]"},
		{"element ref", &ast.ElementRefExpr{Name: "myBtn"},
			`document.querySelector('[data-sngl-id="myBtn"]')`},
		{"list", &ast.ListExpr{
			Elements: []ast.Node{
				&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
				&ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			},
		}, "[1, 2]"},
		{"struct", &ast.StructExpr{
			Name: "Point",
			Fields: []ast.StructFieldLit{
				{Name: "x", Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
				{Name: "y", Value: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}},
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
	tests := []struct {
		name string
		fn   string
		args []ast.Node
		want string
	}{
		{"string()", "string", []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}, "String(42)"},
		{"size()", "size", []ast.Node{&ast.IdentExpr{Name: "count"}}, "state.count.length"},
		{"int()", "int", []ast.Node{&ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}}, "Math.trunc(3.14)"},
		{"float()", "float", []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}, "parseFloat(42)"},
		{"custom()", "myFunc", []ast.Node{
			&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			&ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "myFunc(1, 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateCall(&ast.CallExpr{Func: tt.fn, Args: tt.args}, s)
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
		node ast.Node
		want string
	}{
		{"assign", &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "count"},
			Op:     ast.AssignSet,
			Value:  &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
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
		Parts: []ast.Node{
			&ast.LiteralExpr{Value: "count is ", Kind: ast.LiteralString},
			&ast.IdentExpr{Name: "count"},
		},
	}
	got := translateExpr(node, s)
	want := "`count is ${state.count}`"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
