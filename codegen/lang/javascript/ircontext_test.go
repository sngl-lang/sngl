package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestJsEmitFuncDef_PlainFunc(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	fn := &ir.Func{
		Name:   "greet",
		Params: []*ir.Param{{Name: "name", Type: ir.TypString}},
		Block: []ir.Stmt{
			&ir.Return{Value: &ir.Literal{Type: ir.TypString, Raw: "hi"}},
		},
	}
	got := strings.Join(jc.EmitFuncDef(fn), "\n")
	if !strings.Contains(got, "function greet(name)") {
		t.Errorf("expected 'function greet(name)' header; got: %s", got)
	}
	if !strings.Contains(got, `return "hi"`) {
		t.Errorf("expected return statement; got: %s", got)
	}
}

func TestJsBinary_IntDivisionTruncates(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	expr := &ir.Binary{
		Op:    ast.BinDiv,
		Left:  &ir.Literal{Type: ir.TypInt, Raw: "7"},
		Right: &ir.Literal{Type: ir.TypInt, Raw: "2"},
	}
	got := jc.EvalExpr(expr)
	if got != "Math.trunc(7 / 2)" {
		t.Errorf("got %q, want Math.trunc(7 / 2)", got)
	}
}

func TestJsSelect_I18nPluralKeyConst(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	expr := &ir.Select{
		Operand: &ir.Ident{Name: "i18n"},
		Field:   "other",
	}
	got := jc.EvalExpr(expr)
	if got != `"other"` {
		t.Errorf("got %q, want \"other\"", got)
	}
}

func TestJsCall_HtmlPlacementDirectiveIsIdentity(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	for _, id := range []string{"HtmlFrontend", "HtmlBackend"} {
		call := &ir.Call{
			Func: &ir.Func{Name: id, Intrinsic: id},
			Args: []ir.CallArg{{Value: &ir.Ident{Name: "x"}}},
		}
		got := jc.EvalExpr(call)
		if got != "x" {
			t.Errorf("%s: got %q, want %q (pass-through identity)", id, got, "x")
		}
		if strings.Contains(got, "html.") || strings.Contains(got, id) {
			t.Errorf("%s: directive leaked into output: %q", id, got)
		}
	}
}

func TestJsCall_RegexBuiltin(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func: &ir.Func{Name: "regex"},
		Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "ab+c"}}},
	}
	got := jc.EvalExpr(call)
	if got != `new RegExp("ab+c")` {
		t.Errorf("got %q", got)
	}
}

func TestJsConversion_Bool(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	conv := &ir.Conversion{
		Type:    &ir.Type{Kind: ir.TypeBool},
		Operand: &ir.Ident{Name: "x", Synthesized: true},
	}
	got := jc.EvalExpr(conv)
	if got != "Boolean(x)" {
		t.Errorf("got %q, want Boolean(x)", got)
	}
}

func TestJsMapLit_PluralKeyPlainObject(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	pluralKeyDecl := &ir.StructDef{Name: "PluralKey"}
	pluralKeyType := &ir.Type{Kind: ir.TypeStruct, Decl: pluralKeyDecl}
	mapType := ir.MapOf(pluralKeyType, ir.TypString)
	keyExpr := &ir.Select{
		Operand: &ir.Ident{Name: "i18n"},
		Field:   "one",
	}
	valExpr := &ir.Literal{Raw: "# item", Type: ir.TypString}
	m := &ir.MapLitIR{
		Type: mapType,
		Entries: []ir.MapEntry{
			{Key: keyExpr, Value: valExpr},
		},
	}
	got := jc.EvalExpr(m)
	// i18n.one lowers to the string literal "one"; the PluralKey map lowers to
	// a plain object with that string key. Pin the exact output for parity.
	want := `{["one"]: "# item"}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJsLambda_AsyncPrefix(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	lam := &ir.Lambda{Func: &ir.Func{
		IsAsync: true,
		Params:  []*ir.Param{{Name: "x"}},
		Block:   []ir.Stmt{&ir.Return{Value: &ir.Ident{Name: "x", Synthesized: true}}},
	}}
	got := jc.EvalExpr(lam)
	// Async lambdas always use parens even for a single param (legacy parity):
	// `async (x) => x`, never `async x => x`.
	if got != "async (x) => x" {
		t.Errorf("got %q, want async (x) => x", got)
	}
}

func TestJsLiteral_QuotedScalarTypes(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	// Kind-backed string-domain types.
	for _, k := range []ir.TypeKind{ir.TypeColor} {
		lit := &ir.Literal{Type: &ir.Type{Kind: k}, Raw: "val"}
		got := jc.evalLiteral(lit)
		if got != `"val"` {
			t.Errorf("kind %v: got %q, want \"val\"", k, got)
		}
	}
	// Struct-backed string-representable types (color/date/time/datetime).
	// String-repr is now flag-driven (ir.StructDef.Builtin), not name-driven.
	for name, kind := range map[string]ast.BuiltinKind{
		"color":    ast.BuiltinColor,
		"date":     ast.BuiltinDate,
		"time":     ast.BuiltinTime,
		"datetime": ast.BuiltinDateTime,
	} {
		typ := &ir.Type{Kind: ir.TypeStruct, Decl: &ir.StructDef{Name: name, Builtin: kind}}
		lit := &ir.Literal{Type: typ, Raw: "val"}
		got := jc.evalLiteral(lit)
		if got != `"val"` {
			t.Errorf("struct %s: got %q, want \"val\"", name, got)
		}
	}
}

func TestJsEvalIdent_SynthesizedBareRef(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	got := jc.EvalExpr(&ir.Ident{Name: "__n0", Synthesized: true})
	if got != "__n0" {
		t.Errorf("expected bare '__n0'; got: %s", got)
	}
}

// --- Task 1.10: CreateComponent + namespace user-func dispatch reconciliation ---

// jsFuncTypeNoArgs returns a minimal func-typed ir.Type with no params/return.
func jsFuncTypeNoArgs() *ir.Type {
	return &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{}}
}

// jsIdentToVar builds an *ir.Ident whose Sym is v.
func jsIdentToVar(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v}
}

// TestJsNamespaceCall_CreateComponent mirrors the exact *ir.Call shape produced
// by internal/lower/declarative.go lowerComponentNodeIntoStmts: the Func is the
// CreateComponent intrinsic (Name+Intrinsic="CreateComponent", Receiver=""), the
// Call.Receiver is the `lower` namespace ident, Args=[compIdent, propsLit].
func TestJsNamespaceCall_CreateComponent(t *testing.T) {
	comp := &ir.Component{Name: "Card"}
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	ctx := codegen.NewExprCtx(pkg)
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "lower", Type: ir.TypDyn},
		Func:     &ir.Func{Name: "CreateComponent", Intrinsic: "CreateComponent"},
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "Card", Sym: comp}},
			{Value: &ir.StructLit{}},
		},
	}
	got := jc.EvalExpr(call)
	want := factoryName(comp) + "({})"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestJsNamespaceCall_CreateComponentWithProps confirms props are evaluated and
// passed positionally to the factory.
func TestJsNamespaceCall_CreateComponentWithProps(t *testing.T) {
	comp := &ir.Component{Name: "Badge"}
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	ctx := codegen.NewExprCtx(pkg)
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "lower", Type: ir.TypDyn},
		Func:     &ir.Func{Name: "CreateComponent", Intrinsic: "CreateComponent"},
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "Badge", Sym: comp}},
			{Value: &ir.StructLit{Fields: []ir.FieldInit{
				{Name: "label", Value: &ir.Literal{Type: ir.TypString, Raw: "Clicks"}},
			}}},
		},
	}
	got := jc.EvalExpr(call)
	want := factoryName(comp) + `({label: "Clicks"})`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestJsNamespaceCall_UserFuncDispatch confirms a namespace-qualified user
// function `ns.fn(x)` is emitted as `ns_fn(receiver, x)` — byte-identical to the
// legacy scope.FuncNames path (translate_ir.go:431-433), which joins the receiver
// expression plus the call args.
func TestJsNamespaceCall_UserFuncDispatch(t *testing.T) {
	userFn := &ir.Func{Name: "fn", Receiver: "ns"}
	pkg := &ir.Package{Funcs: []*ir.Func{userFn}}
	ctx := codegen.NewExprCtx(pkg)
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func:     userFn,
		Receiver: &ir.Ident{Name: "ns"},
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "x", Synthesized: true}},
		},
	}
	got := jc.EvalExpr(call)
	want := "ns_fn(ns, x)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestJsNamespaceCall_UserFuncDispatchAsyncAwaits confirms the async wrapping on
// the user-func dispatch path matches legacy (translate_ir.go:441-443).
func TestJsNamespaceCall_UserFuncDispatchAsyncAwaits(t *testing.T) {
	userFn := &ir.Func{Name: "load", Receiver: "ns", IsAsync: true}
	pkg := &ir.Package{Funcs: []*ir.Func{userFn}}
	ctx := codegen.NewExprCtx(pkg)
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func:     userFn,
		Receiver: &ir.Ident{Name: "ns"},
	}
	got := jc.EvalExpr(call)
	want := "await ns_load(ns)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- Task 1.11: ported legacy translate_ir_test.go behaviors ---

func TestJsPlainCall_AwaitsAsyncSNGLCallee(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{Func: &ir.Func{Name: "loadUser", IsAsync: true}}
	got := jc.EvalExpr(call)
	if got != "await loadUser()" {
		t.Errorf("got %q, want await loadUser()", got)
	}
}

func TestJsPlainCall_NoAwaitForSyncCallee(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{Func: &ir.Func{Name: "noop"}}
	got := jc.EvalExpr(call)
	if got != "noop()" {
		t.Errorf("got %q, want noop()", got)
	}
}

func TestJsNamespaceCall_AwaitsAsyncSNGLCallee(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func:     &ir.Func{Name: "fetch", Receiver: "net", IsAsync: true},
		Receiver: &ir.Ident{Name: "net"},
	}
	got := jc.EvalExpr(call)
	if got != "await net.fetch()" {
		t.Errorf("got %q, want await net.fetch()", got)
	}
}

func TestJsNamespaceCall_NoAwaitForSyncCallee(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func:     &ir.Func{Name: "get", Receiver: "store"},
		Receiver: &ir.Ident{Name: "store"},
	}
	got := jc.EvalExpr(call)
	if got != "store.get()" {
		t.Errorf("got %q, want store.get()", got)
	}
}

func TestJsCall_FuncvarAsyncSlot_Awaits(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	v := &ir.Var{Name: "handler", Type: jsFuncTypeNoArgs()}
	pkg := &ir.Package{
		Vars:  []*ir.Var{v},
		Funcs: []*ir.Func{asyncFn},
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotVarKey(v): {asyncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{ir.SlotVarKey(v): ir.ColorAsync},
		},
	}
	ctx := codegen.NewExprCtx(pkg)
	ctx.Locals["handler"] = true
	jc := NewIRContext(ctx)
	call := &ir.Call{Func: nil, Callee: jsIdentToVar(v)}
	got := jc.EvalExpr(call)
	if got != "await handler()" {
		t.Errorf("got %q, want await handler()", got)
	}
}

func TestJsCall_FuncvarSyncSlot_NoAwait(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	v := &ir.Var{Name: "handler", Type: jsFuncTypeNoArgs()}
	pkg := &ir.Package{
		Vars:  []*ir.Var{v},
		Funcs: []*ir.Func{syncFn},
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotVarKey(v): {syncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{ir.SlotVarKey(v): ir.ColorSync},
		},
	}
	ctx := codegen.NewExprCtx(pkg)
	ctx.Locals["handler"] = true
	jc := NewIRContext(ctx)
	call := &ir.Call{Func: nil, Callee: jsIdentToVar(v)}
	got := jc.EvalExpr(call)
	if got != "handler()" {
		t.Errorf("got %q, want handler()", got)
	}
}

func TestJsCall_FuncvarParamSlot_AnyAsync_Awaits(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	p := &ir.Param{Name: "cb"}
	// SlotParam slot has no SlotColor entry → conservative fallback.
	pkg := &ir.Package{
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotParamKey(p): {asyncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{},
		},
	}
	ctx := codegen.NewExprCtx(pkg)
	ctx.Locals["cb"] = true
	jc := NewIRContext(ctx)
	call := &ir.Call{Func: nil, Callee: &ir.Ident{Name: "cb", Sym: p}}
	got := jc.EvalExpr(call)
	if got != "await cb()" {
		t.Errorf("got %q, want await cb()", got)
	}
}

func TestJsEmitI18nTr(t *testing.T) {
	// Mirrors the real IR from inferI18nInterp: ir.Call with no Receiver
	// expression, Func.Receiver="i18n", routed through evalTypeMethodCall.
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func: &ir.Func{Name: "tr", Receiver: "i18n"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Raw: "Hello, {name}!", Type: ir.TypString}},
			{Value: &ir.MapLitIR{}},
		},
	}
	got := jc.EvalExpr(call)
	if !strings.Contains(got, "i18n.getTranslator().tr(") {
		t.Errorf("i18n.tr: got %q, want call containing i18n.getTranslator().tr(", got)
	}
}

func TestJsEmitI18nPlural(t *testing.T) {
	// Namespace call: i18n.plural(count, forms).
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func:     &ir.Func{Name: "plural", Receiver: "i18n"},
		Receiver: &ir.Ident{Name: "i18n"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Raw: "3", Type: ir.TypInt}},
			{Value: &ir.MapLitIR{}},
		},
	}
	got := jc.EvalExpr(call)
	if !strings.Contains(got, "i18n.getTranslator().plural(") {
		t.Errorf("i18n.plural: got %q, want call containing i18n.getTranslator().plural(", got)
	}
}

func TestJsEmitI18nExactly(t *testing.T) {
	// Namespace call: i18n.exactly(n) → ("=" + (n)).
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func:     &ir.Func{Name: "exactly", Receiver: "i18n"},
		Receiver: &ir.Ident{Name: "i18n"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Raw: "0", Type: ir.TypInt}},
		},
	}
	got := jc.EvalExpr(call)
	if !strings.Contains(got, `"=" + `) {
		t.Errorf("i18n.exactly: got %q, want string concat with \"=\"", got)
	}
}

// A component-scoped const is materialized as a per-instance state field
// (e.g. a regex const constructed at runtime), so reads inside component
// code reference it as state.NAME. Package-level consts stay bare top-level
// vars. This mirrors the html legacy path, where main-component vars
// (consts included) are ModelFields while package consts are bare locals.
func TestJsEvalIdent_ConstScoping(t *testing.T) {
	pkgConst := &ir.Var{Name: "PKGC", Type: ir.TypString, IsConst: true}
	compConst := &ir.Var{Name: "ALPHA", Type: ir.TypString, IsConst: true}
	comp := &ir.Component{Name: "main", Vars: []*ir.Var{compConst}}
	pkg := &ir.Package{Consts: []*ir.Var{pkgConst}, Components: []*ir.Component{comp}}

	ctx := codegen.NewExprCtx(pkg)
	jc := NewIRContext(ctx).ForComponent(comp)

	if got := jc.EvalExpr(&ir.Ident{Name: "ALPHA"}); got != "state.ALPHA" {
		t.Errorf("component const: got %q, want state.ALPHA", got)
	}
	if got := jc.EvalExpr(&ir.Ident{Name: "PKGC"}); got != "PKGC" {
		t.Errorf("package const: got %q, want bare PKGC", got)
	}
}

// Element-ref idents: synthesized refs (slot-pipeline __nN / parent vars) and
// locals must stay bare so initial slot render references the in-scope JS var;
// only genuine external user refs (non-synthesized, unresolved) emit a
// querySelector. The current evalIdent keeps synthesized and local refs bare.
func TestJsEvalIdent_ElementRefStaysBare(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)

	// Synthesized element-ref (created __nN node): bare.
	if got := jc.EvalExpr(&ir.Ident{Name: "__n0", IsElementRef: true, Synthesized: true}); got != "__n0" {
		t.Errorf("synthesized element-ref: got %q, want bare __n0", got)
	}
	// Local element-ref (slot-render parent param): bare/renamed.
	jcLocal := NewIRContext(ctx.WithLocal("parent"))
	if got := jcLocal.EvalExpr(&ir.Ident{Name: "parent", IsElementRef: true}); got != "parent" {
		t.Errorf("local element-ref: got %q, want bare parent", got)
	}
}

func TestJsSizedNumericEmission(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	i8 := func(raw string) *ir.Literal { return &ir.Literal{Type: ir.TypInt8, Raw: raw} }
	cases := []struct {
		name string
		expr ir.Expr
		want string
	}{
		{"int8 add masks", &ir.Binary{Type: ir.TypInt8, Op: ast.BinAdd, Left: i8("100"), Right: i8("100")}, "(((100 + 100)) << 24 >> 24)"},
		{"uint8 sub masks", &ir.Binary{Type: ir.TypUint8, Op: ast.BinSub, Left: &ir.Literal{Type: ir.TypUint8, Raw: "0"}, Right: &ir.Literal{Type: ir.TypUint8, Raw: "1"}}, "(((0 - 1)) & 0xFF)"},
		{"uint32 add masks", &ir.Binary{Type: ir.TypUint32, Op: ast.BinAdd, Left: &ir.Literal{Type: ir.TypUint32, Raw: "1"}, Right: &ir.Literal{Type: ir.TypUint32, Raw: "2"}}, "(((1 + 2)) >>> 0)"},
		{"int64 literal is bigint", i8Bit64("5"), "5n"},
		{"uint64 add wraps via asUintN", &ir.Binary{Type: ir.TypUint64, Op: ast.BinAdd, Left: &ir.Literal{Type: ir.TypUint64, Raw: "1"}, Right: &ir.Literal{Type: ir.TypUint64, Raw: "2"}}, "BigInt.asUintN(64, (1n + 2n))"},
		{"float32 literal frounds", &ir.Literal{Type: ir.TypFloat32, Raw: "0.1"}, "Math.fround(0.1)"},
		{"float32 add frounds", &ir.Binary{Type: ir.TypFloat32, Op: ast.BinAdd, Left: &ir.Literal{Type: ir.TypFloat32, Raw: "0.1"}, Right: &ir.Literal{Type: ir.TypFloat32, Raw: "0.2"}}, "Math.fround((Math.fround(0.1) + Math.fround(0.2)))"},
		{"conv to int8 masks", &ir.Conversion{Type: ir.TypInt8, Operand: &ir.Literal{Type: ir.TypInt, Raw: "300"}}, "((300) << 24 >> 24)"},
		{"conv int64 to int8 bridges", &ir.Conversion{Type: ir.TypInt8, Operand: &ir.Literal{Type: ir.TypInt64, Raw: "5000000000"}}, "((Number(5000000000n)) << 24 >> 24)"},
		{"conv number to uint64 lifts", &ir.Conversion{Type: ir.TypUint64, Operand: &ir.Literal{Type: ir.TypInt, Raw: "5"}}, "BigInt.asUintN(64, BigInt(Math.trunc(5)))"},
	}
	for _, tc := range cases {
		if got := jc.EvalExpr(tc.expr); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func i8Bit64(raw string) *ir.Literal { return &ir.Literal{Type: ir.TypInt64, Raw: raw} }
