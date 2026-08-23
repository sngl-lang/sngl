package kotlin

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ktTestCtx builds a KtIRContext over an empty package, matching the
// empty-scope semantics the legacy translate_ir tests exercised.
func ktTestCtx() *KtIRContext {
	return NewIRContext(codegen.NewExprCtx(&ir.Package{}))
}

// TestKtIRContext_I18nTr ports TestEmitI18nTr_Kotlin: the checker emits an
// ir.Call with no Receiver expression and Func.Receiver="i18n" for an i18n.tr
// interpolation. This routes through the type-method call path.
func TestKtIRContext_I18nTr(t *testing.T) {
	fn := &ir.Func{Name: "tr", Receiver: "i18n"}
	keyLit := &ir.Literal{Raw: "Hello, {name}!", Type: ir.TypString}
	argsMap := &ir.MapLitIR{}
	call := &ir.Call{
		Func: fn,
		Args: []ir.CallArg{
			{Value: keyLit},
			{Value: argsMap},
		},
	}
	got := ktTestCtx().EvalExpr(call)
	if !strings.Contains(got, "I18n.getTranslator().tr(") {
		t.Errorf("i18n.tr: got %q, want call containing I18n.getTranslator().tr(", got)
	}
}

// TestKtIRContext_I18nPlural ports TestEmitI18nPlural_Kotlin: i18n.plural is a
// namespace call i18n.plural(count, forms).
func TestKtIRContext_I18nPlural(t *testing.T) {
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
	got := ktTestCtx().EvalExpr(call)
	if !strings.Contains(got, "I18n.getTranslator().plural(") {
		t.Errorf("i18n.plural: got %q, want call containing I18n.getTranslator().plural(", got)
	}
}

// TestKtIRContext_I18nExactly ports TestEmitI18nExactly_Kotlin: i18n.exactly(n)
// lowers to ("=" + (n)).
func TestKtIRContext_I18nExactly(t *testing.T) {
	fn := &ir.Func{Name: "exactly", Receiver: "i18n", Intrinsic: "i18n.exactly"}
	receiverExpr := &ir.Ident{Name: "i18n"}
	nLit := &ir.Literal{Raw: "0", Type: ir.TypInt}
	call := &ir.Call{
		Func:     fn,
		Receiver: receiverExpr,
		Args: []ir.CallArg{
			{Value: nLit},
		},
	}
	got := ktTestCtx().EvalExpr(call)
	if !strings.Contains(got, `"=" + `) {
		t.Errorf("i18n.exactly: got %q, want string concat with \"=\"", got)
	}
}

// TestKtIRContext_PluralKeyMapLit ports
// TestPluralKeyMapLitLowersToStringKeyMap_Kotlin: a map<i18n.PluralKey, string>
// still lowers to mapOf(...) with string keys.
func TestKtIRContext_PluralKeyMapLit(t *testing.T) {
	keyType := pluralKeyType()
	mapType := ir.MapOf(keyType, ir.TypString)

	keyExpr := &ir.Select{
		Operand: &ir.Ident{Name: "i18n"},
		Field:   "one",
		Type:    keyType,
	}
	valExpr := &ir.Literal{Raw: "# item", Type: ir.TypString}

	m := &ir.MapLitIR{
		Type: mapType,
		Entries: []ir.MapEntry{
			{Key: keyExpr, Value: valExpr},
		},
	}
	got := ktTestCtx().EvalExpr(m)
	if !strings.Contains(got, "mapOf(") {
		t.Errorf("PluralKey map: got %q, want mapOf(...)", got)
	}
	if !strings.Contains(got, `"one"`) {
		t.Errorf("PluralKey map: got %q, want key to be string \"one\"", got)
	}
}

// TestKtIRContext_I18nPluralKeyConstants ports
// TestI18nPluralKeyConstantsLowerToStringLiterals_Kotlin: i18n.<key> Selects
// lower to Kotlin string literals.
func TestKtIRContext_I18nPluralKeyConstants(t *testing.T) {
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
			Type:    pluralKeyType(),
		}
		got := ktTestCtx().EvalExpr(sel)
		if got != tc.want {
			t.Errorf("i18n.%s: got %q, want %q", tc.field, got, tc.want)
		}
	}
}

// Every path that emits a generic call refuses a bodyless #[intrinsic] this
// backend has no emitter for. See the javascript twin.
func TestUnimplementedIntrinsicPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		call *ir.Call
	}{
		{"plain call", &ir.Call{Func: &ir.Func{Name: "nope", Intrinsic: "NoBackendHasThis"}}},
		{"method call", &ir.Call{
			Func: &ir.Func{Name: "nope", Receiver: "string", Intrinsic: "NoBackendHasThis"},
			Args: []ir.CallArg{{Value: &ir.Ident{Name: "s", Type: ir.TypString}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic for an intrinsic this backend does not implement")
				}
				if !strings.Contains(r.(string), "NoBackendHasThis") {
					t.Errorf("panic %v does not name the intrinsic", r)
				}
			}()
			ktTestCtx().EvalExpr(tc.call)
		})
	}
}

// A select on a predeclared PluralKey constant is identified by its type, so
// a hand-built one has to carry it.
func pluralKeyType() *ir.Type {
	return &ir.Type{Kind: ir.TypeStruct, Decl: &ir.StructDef{Name: "PluralKey"}}
}
