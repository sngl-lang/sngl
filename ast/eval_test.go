package ast

import "testing"

func strLit(s string) *LiteralExpr {
	return &LiteralExpr{Kind: LiteralStringQuoted, Raw: s}
}

func TestEvalString(t *testing.T) {
	cases := []struct {
		name string
		expr Expr
		want string
		ok   bool
	}{
		{"literal", strLit("color"), "color", true},
		{"concat", &BinaryExpr{Op: BinAdd, Left: strLit("a"), Right: strLit("b")}, "ab", true},
		{"nested concat", &BinaryExpr{Op: BinAdd,
			Left:  &BinaryExpr{Op: BinAdd, Left: strLit("a"), Right: strLit("b")},
			Right: strLit("c")}, "abc", true},
		{"backticked", &LiteralExpr{Kind: LiteralStringBackticked, Raw: "x"}, "x", true},
		{"ident rejected", &IdentExpr{Name: "color"}, "", false},
		{"int literal rejected", &LiteralExpr{Kind: LiteralInt, Raw: "5"}, "", false},
		{"non-add op rejected", &BinaryExpr{Op: BinMul, Left: strLit("a"), Right: strLit("b")}, "", false},
		{"concat with non-string rejected", &BinaryExpr{Op: BinAdd, Left: strLit("a"), Right: &IdentExpr{Name: "x"}}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalString(tc.expr)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error, got %q", got)
			}
			if tc.ok && got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func intLit(s string) *LiteralExpr { return &LiteralExpr{Kind: LiteralInt, Raw: s} }

func TestEvalInt(t *testing.T) {
	cases := []struct {
		name string
		expr Expr
		want int64
		ok   bool
	}{
		{"literal", intLit("42"), 42, true},
		{"negate", &UnaryExpr{Op: UnaryNeg, Operand: intLit("7")}, -7, true},
		{"add", &BinaryExpr{Op: BinAdd, Left: intLit("2"), Right: intLit("3")}, 5, true},
		{"sub", &BinaryExpr{Op: BinSub, Left: intLit("10"), Right: intLit("4")}, 6, true},
		{"mul", &BinaryExpr{Op: BinMul, Left: intLit("6"), Right: intLit("7")}, 42, true},
		{"string rejected", strLit("5"), 0, false},
		{"ident rejected", &IdentExpr{Name: "n"}, 0, false},
		{"div rejected", &BinaryExpr{Op: BinDiv, Left: intLit("6"), Right: intLit("2")}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalInt(tc.expr)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error, got %d", got)
			}
			if tc.ok && got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
