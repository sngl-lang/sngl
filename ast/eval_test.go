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
