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
	if strings.Contains(got, "new Map") {
		t.Errorf("PluralKey map must lower to plain object, got %q", got)
	}
	if !strings.HasPrefix(got, "{[") {
		t.Errorf("expected plain-object form, got %q", got)
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
	if !strings.HasPrefix(got, "async ") {
		t.Errorf("expected async prefix, got %q", got)
	}
}

func TestJsLiteral_QuotedScalarTypes(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	for _, k := range []ir.TypeKind{ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal} {
		lit := &ir.Literal{Type: &ir.Type{Kind: k}, Raw: "val"}
		got := jc.evalLiteral(lit)
		if got != `"val"` {
			t.Errorf("kind %v: got %q, want \"val\"", k, got)
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
