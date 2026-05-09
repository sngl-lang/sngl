package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// funcTypeNoArgs returns a minimal func-typed ir.Type with no params/return.
func funcTypeNoArgs() *ir.Type {
	return &ir.Type{Kind: ir.TypeFunc, Sig: &ir.FuncSig{}}
}

// identToVar builds an *ir.Ident whose Sym is v.
func identToVar(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v}
}

func TestTranslateIRPlainCall_AwaitsAsyncSNGLCallee(t *testing.T) {
	asyncFn := &ir.Func{Name: "loadUser", IsAsync: true}
	call := &ir.Call{Func: asyncFn}
	got := translateIRPlainCall(call, &codegen.ExprScope{})
	want := "await loadUser()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTranslateIRPlainCall_NoAwaitForSyncCallee(t *testing.T) {
	fn := &ir.Func{Name: "noop", IsAsync: false}
	call := &ir.Call{Func: fn}
	got := translateIRPlainCall(call, &codegen.ExprScope{})
	want := "noop()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTranslateIRNamespaceCall_AwaitsAsyncSNGLCallee(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetch", Receiver: "net", IsAsync: true}
	receiverExpr := &ir.Ident{Name: "net"}
	call := &ir.Call{Func: asyncFn, Receiver: receiverExpr}
	got := translateIRNamespaceCall(call, &codegen.ExprScope{})
	want := "await net.fetch()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTranslateIRNamespaceCall_NoAwaitForSyncCallee(t *testing.T) {
	fn := &ir.Func{Name: "get", Receiver: "store"}
	receiverExpr := &ir.Ident{Name: "store"}
	call := &ir.Call{Func: fn, Receiver: receiverExpr}
	got := translateIRNamespaceCall(call, &codegen.ExprScope{})
	want := "store.get()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTranslateIRCall_FuncvarAsyncSlot_Awaits(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	v := &ir.Var{Name: "handler", Type: funcTypeNoArgs()}
	pkg := &ir.Package{
		Vars:  []*ir.Var{v},
		Funcs: []*ir.Func{asyncFn},
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotVarKey(v): {asyncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{ir.SlotVarKey(v): ir.ColorAsync},
		},
	}
	call := &ir.Call{Func: nil, Callee: identToVar(v)}
	scope := &codegen.ExprScope{Pkg: pkg, LocalVars: map[string]bool{"handler": true}}
	got := translateIRCall(call, scope)
	want := "await handler()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTranslateIRCall_FuncvarSyncSlot_NoAwait(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	v := &ir.Var{Name: "handler", Type: funcTypeNoArgs()}
	pkg := &ir.Package{
		Vars:  []*ir.Var{v},
		Funcs: []*ir.Func{syncFn},
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotVarKey(v): {syncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{ir.SlotVarKey(v): ir.ColorSync},
		},
	}
	call := &ir.Call{Func: nil, Callee: identToVar(v)}
	scope := &codegen.ExprScope{Pkg: pkg, LocalVars: map[string]bool{"handler": true}}
	got := translateIRCall(call, scope)
	want := "handler()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEmitI18nTr(t *testing.T) {
	// Build an IR namespace call: i18n.tr("Hello, {name}!", {"name": "Alice"})
	// The namespace receiver is an *ir.Ident("i18n"), the func has Receiver="i18n",Name="tr".
	fn := &ir.Func{Name: "tr", Receiver: "i18n"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	keyLit := &ir.Literal{Raw: "Hello, {name}!", Type: ir.TypString}
	argsMap := &ir.MapLitIR{} // empty map arg for simplicity
	call := &ir.Call{
		Func:     fn,
		Receiver: receiverExpr,
		Args: []ir.CallArg{
			{Value: keyLit},
			{Value: argsMap},
		},
	}
	got := translateIRNamespaceCall(call, &codegen.ExprScope{})
	if !strings.Contains(got, "i18n.getTranslator().tr(") {
		t.Errorf("i18n.tr: got %q, want call containing i18n.getTranslator().tr(", got)
	}
}

func TestEmitI18nPlural(t *testing.T) {
	// i18n.plural is a namespace call: i18n.plural(count, forms)
	fn := &ir.Func{Name: "plural", Receiver: "i18n"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	countLit := &ir.Literal{Raw: "3", Type: ir.TypInt}
	formsMap := &ir.MapLitIR{}
	call := &ir.Call{
		Func:     fn,
		Receiver: receiverExpr,
		Args: []ir.CallArg{
			{Value: countLit},
			{Value: formsMap},
		},
	}
	got := translateIRNamespaceCall(call, &codegen.ExprScope{})
	if !strings.Contains(got, "i18n.getTranslator().plural(") {
		t.Errorf("i18n.plural: got %q, want call containing i18n.getTranslator().plural(", got)
	}
}

func TestEmitI18nExactly(t *testing.T) {
	// i18n.exactly is a namespace call: i18n.exactly(n) → ("=" + (n))
	fn := &ir.Func{Name: "exactly", Receiver: "i18n"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	nLit := &ir.Literal{Raw: "0", Type: ir.TypInt}
	call := &ir.Call{
		Func:     fn,
		Receiver: receiverExpr,
		Args: []ir.CallArg{
			{Value: nLit},
		},
	}
	got := translateIRNamespaceCall(call, &codegen.ExprScope{})
	if !strings.Contains(got, `"=" + `) {
		t.Errorf("i18n.exactly: got %q, want string concat with \"=\"", got)
	}
}

func TestPluralKeyMapLitLowersToPlainObject(t *testing.T) {
	// Build map<i18n.PluralKey, string> with one entry: i18n.one → "one item"
	pluralKeyDecl := &ir.StructDef{Name: "PluralKey"}
	pluralKeyType := &ir.Type{Kind: ir.TypeStruct, Decl: pluralKeyDecl}
	mapType := ir.MapOf(pluralKeyType, ir.TypString)

	// Key: *ir.Select{Operand: *ir.Ident{Name:"i18n"}, Field:"one"}
	// translateIRExpr for this Select will emit `"one"` via jsI18nConstString.
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
	got := translateIRExpr(m, &codegen.ExprScope{})
	// Should emit a plain object, not `new Map(...)`.
	if strings.Contains(got, "new Map") {
		t.Errorf("PluralKey map: got %q, should not use new Map", got)
	}
	if !strings.Contains(got, `"one"`) {
		t.Errorf("PluralKey map: got %q, want key to be string \"one\"", got)
	}
	if !strings.HasPrefix(got, "{") {
		t.Errorf("PluralKey map: got %q, want plain object literal starting with {", got)
	}
}

func TestI18nPluralKeyConstantsLowerToStringLiterals(t *testing.T) {
	cases := []struct {
		field string
		want  string
	}{
		{"zero", `"zero"`},
		{"one", `"one"`},
		{"two", `"two"`},
		{"few", `"few"`},
		{"many", `"many"`},
		{"other", `"other"`},
	}
	for _, tc := range cases {
		sel := &ir.Select{
			Operand: &ir.Ident{Name: "i18n"},
			Field:   tc.field,
		}
		got := translateIRExpr(sel, &codegen.ExprScope{})
		if got != tc.want {
			t.Errorf("i18n.%s: got %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestTranslateIRCall_FuncvarParamSlot_AnyAsync_Awaits(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	p := &ir.Param{Name: "cb"}
	// SlotParam slot has no SlotColor entry → conservative fallback.
	pkg := &ir.Package{
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotParamKey(p): {asyncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{},
		},
	}
	call := &ir.Call{Func: nil, Callee: &ir.Ident{Name: "cb", Sym: p}}
	scope := &codegen.ExprScope{Pkg: pkg, LocalVars: map[string]bool{"cb": true}}
	got := translateIRCall(call, scope)
	want := "await cb()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
