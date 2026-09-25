package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/opeval"
	"git.duckfam.us/jonathan/sngl/ir"
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
	_, ok := numericOp(ast.BinDiv, 1.5, 0.0, opeval.NumKind{})
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
		got, ok := numericOp(tc.op, tc.l, tc.r, opeval.NumKind{})
		if !ok {
			t.Errorf("numericOp(%v, %v, %v, opeval.NumKind{}) returned !ok", tc.op, tc.l, tc.r)
			continue
		}
		if got != tc.want {
			t.Errorf("numericOp(%v, %v, %v, opeval.NumKind{}) = %v, want %v", tc.op, tc.l, tc.r, got, tc.want)
		}
	}
}

func TestNumericOp_NonNumeric(t *testing.T) {
	_, ok := numericOp(ast.BinAdd, "a", "b", opeval.NumKind{})
	if ok {
		t.Error("expected numericOp with strings to return false")
	}
}

func TestEvalUnaryOp_NonBoolNot(t *testing.T) {
	_, ok := evalUnaryOp(ast.UnaryNot, 42, opeval.NumKind{})
	if ok {
		t.Error("expected UnaryNot on int to return false")
	}
}

func TestEvalUnaryOp_NegNonNumeric(t *testing.T) {
	_, ok := evalUnaryOp(ast.UnaryNeg, "hello", opeval.NumKind{})
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

func TestEvalCallFuncZeroArgs(t *testing.T) {
	_, ok := evalCallFunc("string", nil)
	if ok {
		t.Error("expected 0-arg call to return false")
	}
}

func TestEvalBinaryOp_OrBool(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinOr, true, false, opeval.NumKind{})
	if !ok || v != true {
		t.Errorf("true || false = (%v, %v), want (true, true)", v, ok)
	}
}

func TestEvalBinaryOp_AndBool(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinAnd, true, false, opeval.NumKind{})
	if !ok || v != false {
		t.Errorf("true && false = (%v, %v), want (false, true)", v, ok)
	}
}

func TestEvalBinaryOp_NonBoolAnd(t *testing.T) {
	_, ok := evalBinaryOp(ast.BinAnd, "a", "b", opeval.NumKind{})
	if ok {
		t.Error("expected && on strings to fail")
	}
}

func TestEvalBinaryOp_NonBoolOr(t *testing.T) {
	_, ok := evalBinaryOp(ast.BinOr, 1, 2, opeval.NumKind{})
	if ok {
		t.Error("expected || on ints to fail")
	}
}

func TestEvalBinaryOp_Neq(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinNeq, "a", "b", opeval.NumKind{})
	if !ok || v != true {
		t.Errorf("a != b = (%v, %v), want (true, true)", v, ok)
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

func TestEvalQualifiedMethod_ListLength(t *testing.T) {
	v, ok := evalQualifiedMethod("list.length", []any{[]any{1, 2, 3}})
	if !ok || v != 3 {
		t.Errorf("list.length([1,2,3]) = (%v, %v), want (3, true)", v, ok)
	}
}

func TestEvalBinaryOp_StringAdd(t *testing.T) {
	v, ok := evalBinaryOp(ast.BinAdd, "hello", " world", opeval.NumKind{})
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
		got, ok := numericOp(tc.op, tc.l, tc.r, opeval.NumKind{})
		if !ok || got != tc.want {
			t.Errorf("numericOp(%v, %d, %d, opeval.NumKind{}) = (%v, %v), want (%d, true)", tc.op, tc.l, tc.r, got, ok, tc.want)
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

func TestFloatModFolds(t *testing.T) {
	// Float modulo now folds via the shared opeval semantics (the interpreter
	// always computed it with math.Mod; the folder used to bail, a divergence).
	result, ok := numericOp(ast.BinMod, 3.5, 2.0, opeval.NumKind{})
	if !ok || result != 1.5 {
		t.Errorf("numericOp(BinMod, 3.5, 2.0, opeval.NumKind{}) = (%v, %v); want (1.5, true)", result, ok)
	}
}

// --- IR-based evalExpr tests ---

func TestEvalExpr_Literal(t *testing.T) {
	ctx := &evalCtx{platform: "html", language: "js", values: map[ir.Symbol]any{}}
	tests := []struct {
		lit  *ir.Literal
		want any
	}{
		{&ir.Literal{Type: ir.TypInt, Value: "42"}, 42},
		{&ir.Literal{Type: ir.TypFloat, Value: "3.14"}, 3.14},
		{&ir.Literal{Type: ir.TypString, Value: "hello"}, "hello"},
		{&ir.Literal{Type: ir.TypBool, Value: "true"}, true},
		{&ir.Literal{Type: ir.TypBool, Value: "false"}, false},
		{&ir.Literal{Type: ir.TypNull, Value: "null"}, nil},
	}
	for _, tc := range tests {
		got, ok := evalExpr(tc.lit, ctx)
		if !ok {
			t.Errorf("evalExpr(%s) returned !ok", tc.lit.Value)
			continue
		}
		if got != tc.want {
			t.Errorf("evalExpr(%s) = %v, want %v", tc.lit.Value, got, tc.want)
		}
	}
}

// buildTargetConst is what the checker produces for PLATFORM and LANGUAGE: an
// ordinary const carrying the #[builtin] mark that says the compiler supplies
// its value. The evaluator keys off the mark, not the name, so a plain
// Ident{Name: "PLATFORM"} is just a name and does not fold.
func buildTargetConst(name string, kind ir.BuiltinKind) *ir.Ident {
	return &ir.Ident{
		Name: name,
		Type: ir.TypString,
		Sym:  &ir.Var{Name: name, Type: ir.TypString, IsConst: true, Builtin: kind},
	}
}

func TestEvalExpr_Platform(t *testing.T) {
	ctx := &evalCtx{platform: "html", language: "js", values: map[ir.Symbol]any{}}
	got, ok := evalExpr(buildTargetConst("PLATFORM", ir.BuiltinTargetPlatform), ctx)
	if !ok || got != "html" {
		t.Errorf("PLATFORM = (%v, %v), want (html, true)", got, ok)
	}
}

func TestEvalExpr_Language(t *testing.T) {
	ctx := &evalCtx{platform: "html", language: "js", values: map[ir.Symbol]any{}}
	got, ok := evalExpr(buildTargetConst("LANGUAGE", ir.BuiltinTargetLanguage), ctx)
	if !ok || got != "js" {
		t.Errorf("LANGUAGE = (%v, %v), want (js, true)", got, ok)
	}
}

func TestEvalExpr_ConstVar(t *testing.T) {
	v := &ir.Var{Name: "x", IsConst: true, Init: &ir.Literal{Type: ir.TypInt, Value: "42"}}
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	got, ok := evalExpr(&ir.Ident{Name: "x", Type: ir.TypInt, Sym: v}, ctx)
	if !ok || got != 42 {
		t.Errorf("const x = (%v, %v), want (42, true)", got, ok)
	}
}

func TestEvalExpr_NonConstVar(t *testing.T) {
	v := &ir.Var{Name: "x", IsConst: false, Init: &ir.Literal{Type: ir.TypInt, Value: "42"}}
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	_, ok := evalExpr(&ir.Ident{Name: "x", Type: ir.TypInt, Sym: v}, ctx)
	if ok {
		t.Error("expected non-const var to not evaluate")
	}
}

func TestEvalExpr_BinaryAdd(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.Binary{
		Op:    ast.BinAdd,
		Type:  ir.TypInt,
		Left:  &ir.Literal{Type: ir.TypInt, Value: "3"},
		Right: &ir.Literal{Type: ir.TypInt, Value: "4"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != 7 {
		t.Errorf("3 + 4 = (%v, %v), want (7, true)", got, ok)
	}
}

func TestEvalExpr_TernaryTrue(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.Ternary{
		Type: ir.TypString,
		Cond: &ir.Literal{Type: ir.TypBool, Value: "true"},
		Then: &ir.Literal{Type: ir.TypString, Value: "a"},
		Else: &ir.Literal{Type: ir.TypString, Value: "b"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != "a" {
		t.Errorf("true ? a : b = (%v, %v), want (a, true)", got, ok)
	}
}

func TestEvalExpr_TernaryFalse(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.Ternary{
		Type: ir.TypString,
		Cond: &ir.Literal{Type: ir.TypBool, Value: "false"},
		Then: &ir.Literal{Type: ir.TypString, Value: "a"},
		Else: &ir.Literal{Type: ir.TypString, Value: "b"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != "b" {
		t.Errorf("false ? a : b = (%v, %v), want (b, true)", got, ok)
	}
}

func TestEvalExpr_UnaryNeg(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.Unary{
		Op:      ast.UnaryNeg,
		Type:    ir.TypInt,
		Operand: &ir.Literal{Type: ir.TypInt, Value: "42"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != -42 {
		t.Errorf("-42 = (%v, %v), want (-42, true)", got, ok)
	}
}

func TestEvalExpr_UnaryNot(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.Unary{
		Op:      ast.UnaryNot,
		Type:    ir.TypBool,
		Operand: &ir.Literal{Type: ir.TypBool, Value: "true"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != false {
		t.Errorf("!true = (%v, %v), want (false, true)", got, ok)
	}
}

func TestEvalExpr_ListLit(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.ListLit{
		Type: ir.ListOf(ir.TypInt),
		Elems: []ir.Expr{
			&ir.Literal{Type: ir.TypInt, Value: "1"},
			&ir.Literal{Type: ir.TypInt, Value: "2"},
		},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok {
		t.Fatal("expected list eval to succeed")
	}
	list := got.([]any)
	if len(list) != 2 || list[0] != 1 || list[1] != 2 {
		t.Errorf("got %v, want [1, 2]", list)
	}
}

func TestEvalExpr_Conversion(t *testing.T) {
	ctx := &evalCtx{values: map[ir.Symbol]any{}}
	expr := &ir.Conversion{
		Type:    ir.TypInt,
		Operand: &ir.Literal{Type: ir.TypFloat, Value: "3.14"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != 3 {
		t.Errorf("int(3.14) = (%v, %v), want (3, true)", got, ok)
	}
}

func TestEvalExpr_PlatformEq(t *testing.T) {
	ctx := &evalCtx{platform: "html", language: "js", values: map[ir.Symbol]any{}}
	expr := &ir.Binary{
		Op:    ast.BinEq,
		Type:  ir.TypBool,
		Left:  buildTargetConst("PLATFORM", ir.BuiltinTargetPlatform),
		Right: &ir.Literal{Type: ir.TypString, Value: "html"},
	}
	got, ok := evalExpr(expr, ctx)
	if !ok || got != true {
		t.Errorf("PLATFORM == html = (%v, %v), want (true, true)", got, ok)
	}
}

func TestIrLiteral(t *testing.T) {
	tests := []struct {
		val  any
		raw  string
		kind ir.TypeKind
	}{
		{"hello", "hello", ir.TypeString},
		{42, "42", ir.TypeInt},
		{3.14, "3.14", ir.TypeFloat},
		{true, "true", ir.TypeBool},
		{false, "false", ir.TypeBool},
		{nil, "null", ir.TypeNull},
	}
	for _, tc := range tests {
		lit := irLiteral(tc.val, nil)
		if lit == nil {
			t.Errorf("irLiteral(%v) returned nil", tc.val)
			continue
		}
		if lit.Value != tc.raw {
			t.Errorf("irLiteral(%v).Raw = %q, want %q", tc.val, lit.Value, tc.raw)
		}
		if lit.Type.Kind != tc.kind {
			t.Errorf("irLiteral(%v).Type.Kind = %v, want %v", tc.val, lit.Type.Kind, tc.kind)
		}
	}
}
