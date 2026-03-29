package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestToFloat_Int(t *testing.T) {
	f, ok := toFloat(42)
	if !ok || f != 42.0 {
		t.Errorf("toFloat(42) = (%v, %v), want (42.0, true)", f, ok)
	}
}

func TestToFloat_NonNumeric(t *testing.T) {
	_, ok := toFloat("nope")
	if ok {
		t.Error("toFloat(string) should return false")
	}
}

func TestCompareOp_StringComparison(t *testing.T) {
	tests := []struct {
		op   ast.BinaryOp
		l, r string
		want bool
	}{
		{ast.BinLt, "a", "b", true},
		{ast.BinLt, "b", "a", false},
		{ast.BinLte, "a", "a", true},
		{ast.BinLte, "b", "a", false},
		{ast.BinGt, "b", "a", true},
		{ast.BinGt, "a", "b", false},
		{ast.BinGte, "a", "a", true},
		{ast.BinGte, "a", "b", false},
	}
	for _, tc := range tests {
		got, ok := compareOp(tc.op, tc.l, tc.r)
		if !ok {
			t.Errorf("compareOp(%v, %q, %q) returned !ok", tc.op, tc.l, tc.r)
			continue
		}
		if got != tc.want {
			t.Errorf("compareOp(%v, %q, %q) = %v, want %v", tc.op, tc.l, tc.r, got, tc.want)
		}
	}
}

func TestCompareOp_FloatComparison(t *testing.T) {
	tests := []struct {
		op   ast.BinaryOp
		l, r float64
		want bool
	}{
		{ast.BinLt, 1.0, 2.0, true},
		{ast.BinLte, 2.0, 2.0, true},
		{ast.BinGt, 3.0, 2.0, true},
		{ast.BinGte, 2.0, 2.0, true},
	}
	for _, tc := range tests {
		got, ok := compareOp(tc.op, tc.l, tc.r)
		if !ok {
			t.Errorf("compareOp(%v, %v, %v) returned !ok", tc.op, tc.l, tc.r)
			continue
		}
		if got != tc.want {
			t.Errorf("compareOp(%v, %v, %v) = %v, want %v", tc.op, tc.l, tc.r, got, tc.want)
		}
	}
}

func TestCompareOp_Incompatible(t *testing.T) {
	_, ok := compareOp(ast.BinLt, true, "nope")
	if ok {
		t.Error("expected compareOp with incompatible types to return false")
	}
}

func TestFloatDivByZero(t *testing.T) {
	_, ok := numericOp(ast.BinDiv, 1.5, 0.0)
	if ok {
		t.Error("expected float division by zero to return false")
	}
}

func TestNumericOp_FloatArithmetic(t *testing.T) {
	tests := []struct {
		op   ast.BinaryOp
		l, r float64
		want float64
	}{
		{ast.BinAdd, 1.5, 2.5, 4.0},
		{ast.BinSub, 5.0, 2.5, 2.5},
		{ast.BinMul, 2.0, 3.5, 7.0},
		{ast.BinDiv, 10.0, 4.0, 2.5},
	}
	for _, tc := range tests {
		got, ok := numericOp(tc.op, tc.l, tc.r)
		if !ok {
			t.Errorf("numericOp(%v, %v, %v) returned !ok", tc.op, tc.l, tc.r)
			continue
		}
		if got != tc.want {
			t.Errorf("numericOp(%v, %v, %v) = %v, want %v", tc.op, tc.l, tc.r, got, tc.want)
		}
	}
}

func TestNumericOp_NonNumeric(t *testing.T) {
	_, ok := numericOp(ast.BinAdd, "a", "b")
	if ok {
		t.Error("expected numericOp with strings to return false")
	}
}

func TestEvalUnaryOp_NonBoolNot(t *testing.T) {
	_, ok := evalUnaryOp(ast.UnaryNot, 42)
	if ok {
		t.Error("expected UnaryNot on int to return false")
	}
}

func TestEvalUnaryOp_NegNonNumeric(t *testing.T) {
	_, ok := evalUnaryOp(ast.UnaryNeg, "hello")
	if ok {
		t.Error("expected UnaryNeg on string to return false")
	}
}

func TestCallFuncIntFromString(t *testing.T) {
	v, ok := evalCallFunc("int", []any{"42"})
	if !ok || v != 42 {
		t.Errorf("evalCallFunc(int, '42') = (%v, %v), want (42, true)", v, ok)
	}
}

func TestCallFuncIntFromStringError(t *testing.T) {
	_, ok := evalCallFunc("int", []any{"notanint"})
	if ok {
		t.Error("expected int('notanint') to fail")
	}
}

func TestCallFuncFloatFromString(t *testing.T) {
	v, ok := evalCallFunc("float", []any{"3.14"})
	if !ok || v != 3.14 {
		t.Errorf("evalCallFunc(float, '3.14') = (%v, %v), want (3.14, true)", v, ok)
	}
}

func TestCallFuncFloatFromStringError(t *testing.T) {
	_, ok := evalCallFunc("float", []any{"nope"})
	if ok {
		t.Error("expected float('nope') to fail")
	}
}

func TestCallFuncUnknown(t *testing.T) {
	_, ok := evalCallFunc("bogus", []any{1})
	if ok {
		t.Error("expected unknown function to return false")
	}
}

func TestCallFuncIntFromBool(t *testing.T) {
	// int(true) is not supported, falls through
	_, ok := evalCallFunc("int", []any{true})
	if ok {
		t.Error("expected int(true) to fail")
	}
}

func TestCallFuncFloatFromBool(t *testing.T) {
	_, ok := evalCallFunc("float", []any{true})
	if ok {
		t.Error("expected float(true) to fail")
	}
}

func TestEvalMethodStartsWith(t *testing.T) {
	v, ok := evalMethod("startsWith", "hello", []any{"hel"})
	if !ok || v != true {
		t.Errorf("startsWith = (%v, %v), want (true, true)", v, ok)
	}
}

func TestEvalMethodEndsWith(t *testing.T) {
	v, ok := evalMethod("endsWith", "hello", []any{"llo"})
	if !ok || v != true {
		t.Errorf("endsWith = (%v, %v), want (true, true)", v, ok)
	}
}

func TestEvalMethodContains_NonStringArg(t *testing.T) {
	_, ok := evalMethod("contains", "hello", []any{42})
	if ok {
		t.Error("expected contains with non-string arg to fail")
	}
}

func TestEvalMethodStartsWith_NonStringArg(t *testing.T) {
	_, ok := evalMethod("startsWith", "hello", []any{42})
	if ok {
		t.Error("expected startsWith with non-string arg to fail")
	}
}

func TestEvalMethodEndsWith_NonStringArg(t *testing.T) {
	_, ok := evalMethod("endsWith", "hello", []any{42})
	if ok {
		t.Error("expected endsWith with non-string arg to fail")
	}
}

func TestEvalMethodLength_WithArgs(t *testing.T) {
	// string.length("hello", "extra") — dispatched as evalQualifiedMethod
	// which only takes 1 arg for string.length
	_, ok := evalMethod("length", 42, []any{"extra"})
	if ok {
		t.Error("expected length with non-string receiver and extra args to fail")
	}
}

func TestEvalMethodOnNonString(t *testing.T) {
	_, ok := evalMethod("length", 42, nil)
	if ok {
		t.Error("expected length on int to fail")
	}
}

func TestIsConstExpr_Default(t *testing.T) {
	// SelectExpr is an unknown node type for isConstExpr
	node := &ast.SelectExpr{Operand: &ast.IdentExpr{Name: "x"}, Field: "y"}
	vars := map[string]any{"PLATFORM": "html"}
	if isConstExpr(node, vars) {
		t.Error("expected SelectExpr to not be constant")
	}
}

func TestEvalConst_Default(t *testing.T) {
	node := &ast.SelectExpr{Operand: &ast.IdentExpr{Name: "x"}, Field: "y"}
	vars := map[string]any{"PLATFORM": "html"}
	_, ok := evalConst(node, vars)
	if ok {
		t.Error("expected unknown node to not evaluate")
	}
}

func TestEvalBinaryOp_OrBool(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinOr, true, false)
	if !ok || v != true {
		t.Errorf("true || false = (%v, %v), want (true, true)", v, ok)
	}
}

func TestEvalBinaryOp_AndBool(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinAnd, true, false)
	if !ok || v != false {
		t.Errorf("true && false = (%v, %v), want (false, true)", v, ok)
	}
}

func TestEvalBinaryOp_NonBoolAnd(t *testing.T) {
	_, ok := evalBinaryOp(ast.BinAnd, "a", "b")
	if ok {
		t.Error("expected && on strings to fail")
	}
}

func TestEvalBinaryOp_NonBoolOr(t *testing.T) {
	_, ok := evalBinaryOp(ast.BinOr, 1, 2)
	if ok {
		t.Error("expected || on ints to fail")
	}
}

func TestEvalBinaryOp_Neq(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinNeq, "a", "b")
	if !ok || v != true {
		t.Errorf("a != b = (%v, %v), want (true, true)", v, ok)
	}
}

func TestEvalMethodQualified_StringUpper(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.MethodExpr{
				Receiver: &ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString},
				Method:   "upper",
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "HELLO" {
		t.Errorf("expected HELLO, got %v", doc.Data[0].Init.Literal)
	}
}

func TestEvalMethodQualified_StringLower(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.MethodExpr{
				Receiver: &ast.LiteralExpr{Value: "WORLD", Kind: ast.LiteralString},
				Method:   "lower",
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "world" {
		t.Errorf("expected world, got %v", doc.Data[0].Init.Literal)
	}
}

func TestEvalMethodQualified_StringTrim(t *testing.T) {
	doc := &ast.Document{
		Data: []*ast.Data{{
			Name: "val",
			Init: snglExpr(&ast.MethodExpr{
				Receiver: &ast.LiteralExpr{Value: "  hi  ", Kind: ast.LiteralString},
				Method:   "trim",
			}),
		}},
	}
	must(t, Optimize(doc, Config{Platform: "html", Language: "js"}))
	if doc.Data[0].Init.Literal != "hi" {
		t.Errorf("expected 'hi', got %v", doc.Data[0].Init.Literal)
	}
}

func TestEvalQualifiedMethod_IntAbs(t *testing.T) {
	v, ok := evalQualifiedMethod("int.abs", []any{-5})
	if !ok || v != 5 {
		t.Errorf("int.abs(-5) = (%v, %v), want (5, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_FloatFloor(t *testing.T) {
	v, ok := evalQualifiedMethod("float.floor", []any{3.7})
	if !ok || v != 3 {
		t.Errorf("float.floor(3.7) = (%v, %v), want (3, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_FloatCeil(t *testing.T) {
	v, ok := evalQualifiedMethod("float.ceil", []any{3.2})
	if !ok || v != 4 {
		t.Errorf("float.ceil(3.2) = (%v, %v), want (4, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_FloatRound(t *testing.T) {
	v, ok := evalQualifiedMethod("float.round", []any{3.5})
	if !ok || v != 4 {
		t.Errorf("float.round(3.5) = (%v, %v), want (4, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_FloatSqrt(t *testing.T) {
	v, ok := evalQualifiedMethod("float.sqrt", []any{9.0})
	if !ok || v != 3.0 {
		t.Errorf("float.sqrt(9) = (%v, %v), want (3.0, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_StringReplace(t *testing.T) {
	v, ok := evalQualifiedMethod("string.replace", []any{"hello world", "world", "go"})
	if !ok || v != "hello go" {
		t.Errorf("string.replace = (%v, %v), want ('hello go', true)", v, ok)
	}
}

func TestEvalQualifiedMethod_StringIndexOf(t *testing.T) {
	v, ok := evalQualifiedMethod("string.indexOf", []any{"hello", "ll"})
	if !ok || v != 2 {
		t.Errorf("string.indexOf = (%v, %v), want (2, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_StringSubstring(t *testing.T) {
	v, ok := evalQualifiedMethod("string.substring", []any{"hello", 1, 4})
	if !ok || v != "ell" {
		t.Errorf("string.substring = (%v, %v), want ('ell', true)", v, ok)
	}
}

func TestEvalQualifiedMethod_Unknown(t *testing.T) {
	_, ok := evalQualifiedMethod("string.bogus", []any{"hello"})
	if ok {
		t.Error("expected unknown qualified method to fail")
	}
}

func TestEvalQualifiedMethod_FloatPow(t *testing.T) {
	v, ok := evalQualifiedMethod("float.pow", []any{2.0, 3.0})
	if !ok || v != 8.0 {
		t.Errorf("float.pow(2,3) = (%v, %v), want (8.0, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_FloatTrig(t *testing.T) {
	for _, name := range []string{"float.sin", "float.cos", "float.tan", "float.asin", "float.acos", "float.atan"} {
		_, ok := evalQualifiedMethod(name, []any{0.5})
		if !ok {
			t.Errorf("%s(0.5) returned !ok", name)
		}
	}
}

func TestEvalQualifiedMethod_FloatAtan2(t *testing.T) {
	_, ok := evalQualifiedMethod("float.atan2", []any{1.0, 1.0})
	if !ok {
		t.Error("float.atan2(1,1) returned !ok")
	}
}

func TestIsConstExpr_MethodExprTypeNS(t *testing.T) {
	// Type-namespace method call: string.length("hi")
	vars := map[string]any{}
	node := &ast.MethodExpr{
		Receiver: &ast.IdentExpr{Name: "string"},
		Method:   "length",
		Args:     []ast.Node{&ast.LiteralExpr{Value: "hi", Kind: ast.LiteralString}},
	}
	if !isConstExpr(node, vars) {
		t.Error("expected type-ns method call to be constant")
	}
}

func TestIsConstExpr_MethodExprNonConstReceiver(t *testing.T) {
	vars := map[string]any{}
	node := &ast.MethodExpr{
		Receiver: &ast.IdentExpr{Name: "count"},
		Method:   "toString",
	}
	if isConstExpr(node, vars) {
		t.Error("expected non-const receiver method to not be constant")
	}
}

func TestEvalConst_TernaryNonBoolCond(t *testing.T) {
	vars := map[string]any{"PLATFORM": "html"}
	node := &ast.TernaryExpr{
		Cond: &ast.LiteralExpr{Value: "notbool", Kind: ast.LiteralString},
		Then: &ast.LiteralExpr{Value: "a", Kind: ast.LiteralString},
		Else: &ast.LiteralExpr{Value: "b", Kind: ast.LiteralString},
	}
	_, ok := evalConst(node, vars)
	if ok {
		t.Error("expected ternary with non-bool condition to fail")
	}
}

func TestEvalConst_MethodExprNonConstArg(t *testing.T) {
	vars := map[string]any{}
	node := &ast.MethodExpr{
		Receiver: &ast.LiteralExpr{Value: "hello", Kind: ast.LiteralString},
		Method:   "contains",
		Args:     []ast.Node{&ast.IdentExpr{Name: "unknown"}},
	}
	_, ok := evalConst(node, vars)
	if ok {
		t.Error("expected method with non-const arg to fail")
	}
}

func TestEvalConst_CallExprNonConstArg(t *testing.T) {
	vars := map[string]any{}
	node := &ast.CallExpr{
		Func: "string",
		Args: []ast.Node{&ast.IdentExpr{Name: "unknown"}},
	}
	_, ok := evalConst(node, vars)
	if ok {
		t.Error("expected call with non-const arg to fail")
	}
}

func TestEvalConst_InterpolationNonConstPart(t *testing.T) {
	vars := map[string]any{}
	node := &ast.InterpolationExpr{
		Parts: []ast.Node{
			&ast.LiteralExpr{Value: "x=", Kind: ast.LiteralString},
			&ast.IdentExpr{Name: "unknown"},
		},
	}
	_, ok := evalConst(node, vars)
	if ok {
		t.Error("expected interpolation with non-const part to fail")
	}
}

func TestEvalMethodQualified_ListLength(t *testing.T) {
	v, ok := evalQualifiedMethod("list.length", []any{[]any{1, 2, 3}})
	if !ok || v != 3 {
		t.Errorf("list.length([1,2,3]) = (%v, %v), want (3, true)", v, ok)
	}
}

func TestEvalQualifiedMethod_SubstringBounds(t *testing.T) {
	// start < 0 clamps to 0, end > len clamps to len
	v, ok := evalQualifiedMethod("string.substring", []any{"hello", -1, 100})
	if !ok || v != "hello" {
		t.Errorf("substring with out of bounds = (%v, %v), want ('hello', true)", v, ok)
	}
	// start > end returns empty
	v, ok = evalQualifiedMethod("string.substring", []any{"hello", 3, 1})
	if !ok || v != "" {
		t.Errorf("substring with start>end = (%v, %v), want ('', true)", v, ok)
	}
}

func TestEvalBinaryOp_StringAdd(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinAdd, "hello", " world")
	if !ok || v != "hello world" {
		t.Errorf("'hello' + ' world' = (%v, %v)", v, ok)
	}
}

func TestEvalBinaryOp_IntArithmetic(t *testing.T) {
	tests := []struct {
		op   ast.BinaryOp
		l, r int
		want int
	}{
		{ast.BinAdd, 1, 2, 3},
		{ast.BinSub, 5, 3, 2},
		{ast.BinMul, 3, 4, 12},
		{ast.BinDiv, 10, 3, 3},
		{ast.BinMod, 10, 3, 1},
	}
	for _, tc := range tests {
		got, ok := numericOp(tc.op, tc.l, tc.r)
		if !ok || got != tc.want {
			t.Errorf("numericOp(%v, %d, %d) = (%v, %v), want (%d, true)", tc.op, tc.l, tc.r, got, ok, tc.want)
		}
	}
}

func TestCompareOp_IntComparison(t *testing.T) {
	tests := []struct {
		op   ast.BinaryOp
		l, r int
		want bool
	}{
		{ast.BinLt, 1, 2, true},
		{ast.BinLte, 2, 2, true},
		{ast.BinGt, 3, 2, true},
		{ast.BinGte, 2, 2, true},
		{ast.BinLt, 2, 1, false},
	}
	for _, tc := range tests {
		got, ok := compareOp(tc.op, tc.l, tc.r)
		if !ok || got != tc.want {
			t.Errorf("compareOp(%v, %d, %d) = (%v, %v), want (%v, true)", tc.op, tc.l, tc.r, got, ok, tc.want)
		}
	}
}
