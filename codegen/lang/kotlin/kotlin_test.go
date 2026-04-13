package kotlin

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

func mkArgs(exprs ...ast.Expr) ast.ArgList {
	var args []ast.ArgOrEventHandler
	for _, e := range exprs {
		args = append(args, ast.Arg{Value: e})
	}
	return ast.ArgList{Args: args}
}

func TestTranslateExpr(t *testing.T) {
	s := scope()
	tests := []struct {
		name string
		node ast.Expr
		want string
	}{
		{"int literal", &ast.LiteralExpr{Raw: "42", Kind: ast.LiteralInt}, "42"},
		{"float literal", &ast.LiteralExpr{Raw: "3.14", Kind: ast.LiteralFloat}, "3.14"},
		{"string literal", &ast.LiteralExpr{Raw: "hello", Kind: ast.LiteralStringQuoted}, `"hello"`},
		{"bool true", &ast.LiteralExpr{Raw: "true", Kind: ast.LiteralBool}, "true"},
		{"bool false", &ast.LiteralExpr{Raw: "false", Kind: ast.LiteralBool}, "false"},
		{"null", &ast.LiteralExpr{Raw: "", Kind: ast.LiteralNull}, "null"},
		{"model field", &ast.IdentExpr{Name: "count"}, "count"},
		{"computed field", &ast.IdentExpr{Name: "doubled"}, "doubled"},
		{"local var with rename", &ast.IdentExpr{Name: "item"}, "item_0"},
		{"binary add", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt},
			Op:    ast.BinAdd,
			Right: &ast.LiteralExpr{Raw: "2", Kind: ast.LiteralInt},
		}, "(1 + 2)"},
		{"binary eq", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt},
			Op:    ast.BinEq,
			Right: &ast.LiteralExpr{Raw: "2", Kind: ast.LiteralInt},
		}, "(1 == 2)"},
		{"unary not", &ast.UnaryExpr{
			Op:      ast.UnaryNot,
			Operand: &ast.LiteralExpr{Raw: "true", Kind: ast.LiteralBool},
		}, "!true"},
		{"ternary", &ast.TernaryExpr{
			Cond: &ast.LiteralExpr{Raw: "true", Kind: ast.LiteralBool},
			Then: &ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt},
			Else: &ast.LiteralExpr{Raw: "2", Kind: ast.LiteralInt},
		}, "(if (true) 1 else 2)"},
		{"select", &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Field:   "value",
		}, "count.value"},
		{"index", &ast.IndexExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Index:   &ast.LiteralExpr{Raw: "0", Kind: ast.LiteralInt},
		}, "count[0]"},
		{"list", &ast.ListExpr{
			Elements: []ast.Expr{
				&ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt},
				&ast.LiteralExpr{Raw: "2", Kind: ast.LiteralInt},
			},
		}, "listOf(1, 2)"},
		{"struct", &ast.StructExpr{
			Name: "point",
			Fields: []ast.StructFieldLit{
				{Name: "x", Value: &ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt}},
				{Name: "y", Value: &ast.LiteralExpr{Raw: "2", Kind: ast.LiteralInt}},
			},
		}, "Point(x = 1, y = 2)"},
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
		args []ast.Expr
		want string
	}{
		{"string()", "string", []ast.Expr{&ast.LiteralExpr{Raw: "42", Kind: ast.LiteralInt}}, "42.toString()"},
		{"int()", "int", []ast.Expr{&ast.LiteralExpr{Raw: "3.14", Kind: ast.LiteralFloat}}, "3.14.toInt()"},
		{"float()", "float", []ast.Expr{&ast.LiteralExpr{Raw: "42", Kind: ast.LiteralInt}}, "42.toDouble()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateCall(&ast.CallExpr{
				Func: &ast.IdentExpr{Name: tt.fn},
				Args: mkArgs(tt.args...),
			}, s)
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
			&ast.LiteralExpr{Raw: `"count is "`, Kind: ast.LiteralStringQuoted},
			&ast.IdentExpr{Name: "count"},
		},
	}
	got := translateExpr(node, s)
	want := `"count is ${count}"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTranslateMutation(t *testing.T) {
	s := scope()
	tests := []struct {
		name string
		node ast.Stmt
		want []string
	}{
		{"assign", &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "count"},
			Op:     ast.AssignSet,
			Value:  &ast.LiteralExpr{Raw: "0", Kind: ast.LiteralInt},
		}, []string{"count = 0"}},
		{"add assign", &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "count"},
			Op:     ast.AssignAdd,
			Value:  &ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt},
		}, []string{"count += 1"}},
		{"toggle", &ast.ToggleStmt{
			Target: &ast.IdentExpr{Name: "count"},
		}, []string{"count = !count"}},
		{"push", &ast.CallStmt{
			Call: &ast.CallExpr{
				Func: &ast.SelectExpr{
					Operand: &ast.IdentExpr{Name: "count"},
					Field:   "push",
				},
				Args: mkArgs(&ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt}),
			},
		}, []string{"count.add(1)"}},
		{"remove", &ast.CallStmt{
			Call: &ast.CallExpr{
				Func: &ast.SelectExpr{
					Operand: &ast.IdentExpr{Name: "count"},
					Field:   "remove",
				},
				Args: mkArgs(&ast.LiteralExpr{Raw: "0", Kind: ast.LiteralInt}),
			},
		}, []string{"count.removeAt(0)"}},
		{"emit", &ast.EmitStmt{
			Name: "click",
			Args: mkArgs(&ast.LiteralExpr{Raw: "42", Kind: ast.LiteralInt}),
		}, []string{"onClick?.invoke(42)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateMutation(tt.node, s)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d stmts, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("stmt[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExportName(t *testing.T) {
	tr := &Translator{}
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"count", "count"},
		{"myField", "myField"},
	}
	for _, tt := range tests {
		got := tr.ExportName(tt.in)
		if got != tt.want {
			t.Errorf("ExportName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTypeToNative(t *testing.T) {
	tr := &Translator{}
	tests := []struct {
		hint, want string
	}{
		{"int", "Int"},
		{"float", "Double"},
		{"bool", "Boolean"},
		{"string", "String"},
		{"color", "String"},
		{"url", "String"},
		{"unknown", "Any"},
	}
	for _, tt := range tests {
		got := tr.TypeToNative(tt.hint)
		if got != tt.want {
			t.Errorf("TypeToNative(%q) = %q, want %q", tt.hint, got, tt.want)
		}
	}
}

func TestKotlinBuiltinMethod(t *testing.T) {
	s := scope()
	tests := []struct {
		name     string
		receiver ast.Expr
		method   string
		args     []ast.Expr
		want     string
	}{
		{"int.min", &ast.IdentExpr{Name: "int"}, "min",
			[]ast.Expr{&ast.LiteralExpr{Raw: "1", Kind: ast.LiteralInt}, &ast.LiteralExpr{Raw: "2", Kind: ast.LiteralInt}},
			"minOf(1, 2)"},
		{"int.abs", &ast.IdentExpr{Name: "int"}, "abs",
			[]ast.Expr{&ast.LiteralExpr{Raw: "5", Kind: ast.LiteralInt}},
			"kotlin.math.abs(5)"},
		{"int.clamp", &ast.IdentExpr{Name: "int"}, "clamp",
			[]ast.Expr{
				&ast.LiteralExpr{Raw: "5", Kind: ast.LiteralInt},
				&ast.LiteralExpr{Raw: "0", Kind: ast.LiteralInt},
				&ast.LiteralExpr{Raw: "10", Kind: ast.LiteralInt},
			},
			"5.coerceIn(0, 10)"},
		{"string.upper", &ast.IdentExpr{Name: "string"}, "upper",
			[]ast.Expr{&ast.LiteralExpr{Raw: "hello", Kind: ast.LiteralStringQuoted}},
			`"hello".uppercase()`},
		{"string.length", &ast.IdentExpr{Name: "string"}, "length",
			[]ast.Expr{&ast.LiteralExpr{Raw: "hello", Kind: ast.LiteralStringQuoted}},
			`"hello".length`},
		{"list.length", &ast.IdentExpr{Name: "list"}, "length",
			[]ast.Expr{&ast.IdentExpr{Name: "count"}},
			"count.size"},
		{"list.join", &ast.IdentExpr{Name: "list"}, "join",
			[]ast.Expr{&ast.IdentExpr{Name: "count"}, &ast.LiteralExpr{Raw: ", ", Kind: ast.LiteralStringQuoted}},
			`count.joinToString(", ")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := &ast.SelectExpr{
				Operand: tt.receiver,
				Field:   tt.method,
			}
			got := kotlinBuiltinMethod(sel, tt.args, s)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
