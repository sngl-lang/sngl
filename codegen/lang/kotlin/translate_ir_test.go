package kotlin

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitI18nTr_Kotlin(t *testing.T) {
	// Mirror the real IR shape produced by inferI18nInterp in internal/checker/i18n.go:
	// the checker emits an ir.Call with no Receiver expression — only Func.Receiver="i18n".
	// That routes through translateIRTypeMethodCall (not translateIRNamespaceCall).
	// Args are [keyLit, argsMap]; kotlinBuiltinMethodFromArgs("i18n.tr", args) passes
	// key as both the manifest-key and inlined-template positions.
	fn := &ir.Func{Name: "tr", Receiver: "i18n"}
	keyLit := &ir.Literal{Raw: "Hello, {name}!", Type: ir.TypString}
	argsMap := &ir.MapLitIR{} // empty map arg for simplicity
	call := &ir.Call{
		Func: fn,
		Args: []ir.CallArg{
			{Value: keyLit},
			{Value: argsMap},
		},
	}
	got := translateIRCall(call, &codegen.ExprScope{})
	if !strings.Contains(got, "I18n.getTranslator().tr(") {
		t.Errorf("i18n.tr: got %q, want call containing I18n.getTranslator().tr(", got)
	}
}

func TestEmitI18nPlural_Kotlin(t *testing.T) {
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
	if !strings.Contains(got, "I18n.getTranslator().plural(") {
		t.Errorf("i18n.plural: got %q, want call containing I18n.getTranslator().plural(", got)
	}
}

func TestEmitI18nExactly_Kotlin(t *testing.T) {
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

func TestPluralKeyMapLitLowersToStringKeyMap_Kotlin(t *testing.T) {
	// Build map<i18n.PluralKey, string> with one entry: i18n.one → "one item"
	pluralKeyDecl := &ir.StructDef{Name: "PluralKey"}
	pluralKeyType := &ir.Type{Kind: ir.TypeStruct, Decl: pluralKeyDecl}
	mapType := ir.MapOf(pluralKeyType, ir.TypString)

	// Key: *ir.Select{Operand: *ir.Ident{Name:"i18n"}, Field:"one"}
	// translateIRExpr for this Select will emit `"one"` via kotlinI18nConstString.
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
	// Should still emit mapOf(...) — Kotlin uses mapOf for all maps.
	if !strings.Contains(got, "mapOf(") {
		t.Errorf("PluralKey map: got %q, want mapOf(...)", got)
	}
	// Key should be the string literal "one", not a member access expression.
	if !strings.Contains(got, `"one"`) {
		t.Errorf("PluralKey map: got %q, want key to be string \"one\"", got)
	}
}

func TestI18nPluralKeyConstantsLowerToStringLiterals_Kotlin(t *testing.T) {
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
