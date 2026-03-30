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
		{"model field", &ast.IdentExpr{Name: "count"}, "count"},
		{"computed field", &ast.IdentExpr{Name: "doubled"}, "doubled"},
		{"local var with rename", &ast.IdentExpr{Name: "item"}, "item_0"},
		{"binary add", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Op:    ast.BinAdd,
			Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "(1 + 2)"},
		{"binary eq", &ast.BinaryExpr{
			Left:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Op:    ast.BinEq,
			Right: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "(1 == 2)"},
		{"unary not", &ast.UnaryExpr{
			Op:      ast.UnaryNot,
			Operand: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
		}, "!true"},
		{"ternary", &ast.TernaryExpr{
			Cond: &ast.LiteralExpr{Value: true, Kind: ast.LiteralBool},
			Then: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
			Else: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
		}, "(if (true) 1 else 2)"},
		{"select", &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Field:   "value",
		}, "count.value"},
		{"index", &ast.IndexExpr{
			Operand: &ast.IdentExpr{Name: "count"},
			Index:   &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
		}, "count[0]"},
		{"list", &ast.ListExpr{
			Elements: []ast.Node{
				&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
				&ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt},
			},
		}, "listOf(1, 2)"},
		{"struct", &ast.StructExpr{
			Name: "point",
			Fields: []ast.StructFieldLit{
				{Name: "x", Value: &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
				{Name: "y", Value: &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}},
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
		args []ast.Node
		want string
	}{
		{"string()", "string", []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}, "42.toString()"},
		{"int()", "int", []ast.Node{&ast.LiteralExpr{Value: 3.14, Kind: ast.LiteralFloat}}, "3.14.toInt()"},
		{"float()", "float", []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}}, "42.toDouble()"},
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
	want := `"count is ${count}"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTranslateMutation(t *testing.T) {
	s := scope()
	tests := []struct {
		name string
		node ast.Node
		want []string
	}{
		{"assign", &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "count"},
			Op:     ast.AssignSet,
			Value:  &ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
		}, []string{"count = 0"}},
		{"add assign", &ast.AssignStmt{
			Target: &ast.IdentExpr{Name: "count"},
			Op:     ast.AssignAdd,
			Value:  &ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt},
		}, []string{"count += 1"}},
		{"toggle", &ast.ToggleStmt{
			Target: &ast.IdentExpr{Name: "count"},
		}, []string{"count = !count"}},
		{"push", &ast.MethodExpr{
			Receiver: &ast.IdentExpr{Name: "count"},
			Method:   "push",
			Args:     []ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}},
		}, []string{"count.add(1)"}},
		{"remove", &ast.MethodExpr{
			Receiver: &ast.IdentExpr{Name: "count"},
			Method:   "remove",
			Args:     []ast.Node{&ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt}},
		}, []string{"count.removeAt(0)"}},
		{"emit", &ast.EmitStmt{
			Name: "click",
			Args: []ast.Node{&ast.LiteralExpr{Value: 42, Kind: ast.LiteralInt}},
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
		receiver ast.Node
		method   string
		args     []ast.Node
		want     string
	}{
		{"int.min", &ast.IdentExpr{Name: "int"}, "min",
			[]ast.Node{&ast.LiteralExpr{Value: 1, Kind: ast.LiteralInt}, &ast.LiteralExpr{Value: 2, Kind: ast.LiteralInt}},
			"minOf(1, 2)"},
		{"int.abs", &ast.IdentExpr{Name: "int"}, "abs",
			[]ast.Node{&ast.LiteralExpr{Value: 5, Kind: ast.LiteralInt}},
			"kotlin.math.abs(5)"},
		{"int.clamp", &ast.IdentExpr{Name: "int"}, "clamp",
			[]ast.Node{
				&ast.LiteralExpr{Value: 5, Kind: ast.LiteralInt},
				&ast.LiteralExpr{Value: 0, Kind: ast.LiteralInt},
				&ast.LiteralExpr{Value: 10, Kind: ast.LiteralInt},
			},
			"5.coerceIn(0, 10)"},
		{"string.upper", &ast.IdentExpr{Name: "string"}, "upper",
			[]ast.Node{&ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString}},
			`"hello".uppercase()`},
		{"string.length", &ast.IdentExpr{Name: "string"}, "length",
			[]ast.Node{&ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString}},
			`"hello".length`},
		{"list.length", &ast.IdentExpr{Name: "list"}, "length",
			[]ast.Node{&ast.IdentExpr{Name: "count"}},
			"count.size"},
		{"list.join", &ast.IdentExpr{Name: "list"}, "join",
			[]ast.Node{&ast.IdentExpr{Name: "count"}, &ast.LiteralExpr{Value: ", ", Kind: ast.LiteralString}},
			`count.joinToString(", ")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &ast.MethodExpr{
				Receiver: tt.receiver,
				Method:   tt.method,
				Args:     tt.args,
			}
			got := kotlinBuiltinMethod(node, s)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
