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
		node ast.Expr
		want string
	}{
		{"int literal", &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "42"}, "42"},
		{"float literal", &ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: "3.14"}, "3.14"},
		{"string literal", &ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: `"hello"`}, `"\"hello\""`},
		{"bool true", &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "true"}, "true"},
		{"bool false", &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "false"}, "false"},
		{"null", &ast.LiteralExpr{Kind: ast.LiteralNull, Raw: "null"}, "nil"},
		{"model field", &ast.IdentExpr{Name: "count"}, "m.Count"},
		{"computed field", &ast.IdentExpr{Name: "doubled"}, "m.doubled()"},
		{"local var with rename", &ast.IdentExpr{Name: "item"}, "item_0"},
		{"binary add", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
			Op:    ast.BinAdd,
			Right: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
		}, "(1 + 2)"},
		{"unary not", &ast.UnaryExpr{
			Op:      ast.UnaryNot,
			Operand: &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "true"},
		}, "!true"},
		{"ternary", &ast.TernaryExpr{
			Cond: &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: "true"},
			Then: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
			Else: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
		}, "ternary(true, 1, 2)"},
		{"select", &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Field:   "value",
		}, "m.Count.Value"},
		{"index", &ast.IndexExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Index:   &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "0"},
		}, "m.Count[0]"},
		{"element ref", &ast.ElementRefExpr{Name: "myBtn"}, `elementRef("myBtn")`},
		{"list", &ast.ListExpr{
			Elements: []ast.Expr{
				&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"},
				&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"},
			},
		}, "[]any{1, 2}"},
		{"struct", &ast.StructExpr{
			Name: "point",
			Fields: []ast.StructFieldLit{
				{Name: "x", Value: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "1"}},
				{Name: "y", Value: &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "2"}},
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
		args []ast.Expr
		want string
	}{
		{"string()", "string", []ast.Expr{&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "42"}}, "fmt.Sprint(42)"},
		{"int()", "int", []ast.Expr{&ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: "3.14"}}, "int(3.14)"},
		{"float()", "float", []ast.Expr{&ast.LiteralExpr{Kind: ast.LiteralInt, Raw: "42"}}, "float64(42)"},
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

func TestTranslateInterpolation(t *testing.T) {
	s := scope()
	node := &ast.InterpolationExpr{
		Parts: []ast.Expr{
			&ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: "count is "},
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
