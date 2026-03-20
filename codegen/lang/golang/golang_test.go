package golang

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
		{"null", &ast.LiteralExpr{Value: nil, Kind: ast.LiteralNull}, "nil"},
		{"model field", &ast.IdentExpr{Name: "count"}, "m.Count"},
		{"computed field", &ast.IdentExpr{Name: "doubled"}, "m.doubled()"},
		{"local var with rename", &ast.IdentExpr{Name: "item"}, "item_0"},
		{"binary add", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Op:    ast.BinAdd,
			Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "(1 + 2)"},
		{"unary not", &ast.UnaryExpr{
			Op:      ast.UnaryNot,
			Operand: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
		}, "!true"},
		{"ternary", &ast.TernaryExpr{
			Cond: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
			Then: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Else: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "ternary(true, 1, 2)"},
		{"select", &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Field:   "value",
		}, "m.Count.value"},
		{"index", &ast.IndexExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Index:   &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
		}, "m.Count[0]"},
		{"element ref", &ast.ElementRefExpr{Name: "myBtn"}, `elementRef("myBtn")`},
		{"list", &ast.ListExpr{
			Elements: []ast.Node{
				&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
				&ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			},
		}, "[]any{1, 2}"},
		{"struct", &ast.StructExpr{
			Name: "point",
			Fields: []ast.StructFieldLit{
				{Name: "x", Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
				{Name: "y", Value: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}},
			},
		}, "Point{X: 1, Y: 2}"},
		{"nil expr", nil, "nil"},
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
		{"string()", "string", []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}, "fmt.Sprint(42)"},
		{"size()", "size", []ast.Node{&ast.IdentExpr{Name: "count"}}, "len(m.Count)"},
		{"int()", "int", []ast.Node{&ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}}, "int(3.14)"},
		{"float()", "float", []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}, "float64(42)"},
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

func TestTranslateInterpolation(t *testing.T) {
	s := scope()
	node := &ast.InterpolationExpr{
		Parts: []ast.Node{
			&ast.LiteralExpr{Value: "count is ", Kind: ast.LiteralString},
			&ast.IdentExpr{Name: "count"},
		},
	}
	got := translateExpr(node, s)
	want := `fmt.Sprintf("count is %v", m.Count)`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExportName(t *testing.T) {
	tr := &Translator{}
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"count", "Count"},
		{"myField", "MyField"},
	}
	for _, tt := range tests {
		got := tr.ExportName(tt.in)
		if got != tt.want {
			t.Errorf("ExportName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
