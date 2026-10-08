package codegen

import (
	"testing"

	"duckfam.us/sngl/ir"
)

func TestIntrinsicRegistry(t *testing.T) {
	const lang = "test-lang-reg"
	RegisterIntrinsic(lang, "Doubler", func(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
		return tr(args[0]) + " * 2", []string{"mathlib"}
	})

	if LookupIntrinsic(lang, "Doubler") == nil {
		t.Fatal("registered emitter not found")
	}
	if LookupIntrinsic(lang, "Missing") != nil {
		t.Error("unregistered id returned an emitter")
	}
	if LookupIntrinsic(lang, "") != nil {
		t.Error("empty id must return nil")
	}

	// EmitIntrinsicCall dispatches by the call's Func.Intrinsic.
	call := &ir.Call{
		Func: &ir.Func{Intrinsic: "Doubler"},
		Args: []ir.CallArg{{Value: &ir.Ident{Name: "x"}}},
	}
	out, imports, ok := EmitIntrinsicCall(lang, "", call, func(e ir.Expr) string {
		return e.(*ir.Ident).Name
	})
	if !ok || out != "x * 2" {
		t.Errorf("EmitIntrinsicCall = (%q, %v); want (\"x * 2\", true)", out, ok)
	}
	if len(imports) != 1 || imports[0] != "mathlib" {
		t.Errorf("imports = %v; want [mathlib]", imports)
	}

	// A call with no intrinsic, or an unknown lang, declines.
	if _, _, ok := EmitIntrinsicCall(lang, "", &ir.Call{Func: &ir.Func{}}, nil); ok {
		t.Error("call without an intrinsic ID must decline")
	}
	if _, _, ok := EmitIntrinsicCall("other-lang", "", call, func(e ir.Expr) string { return "" }); ok {
		t.Error("unknown lang must decline")
	}
}

func TestIntrinsicRegistryDuplicatePanics(t *testing.T) {
	const lang = "test-lang-dup"
	RegisterIntrinsic(lang, "X", func([]ir.Expr, func(ir.Expr) string) (string, []string) { return "", nil })
	defer func() {
		if recover() == nil {
			t.Error("duplicate registration did not panic")
		}
	}()
	RegisterIntrinsic(lang, "X", func([]ir.Expr, func(ir.Expr) string) (string, []string) { return "", nil })
}

func TestRequireIntrinsicFallback(t *testing.T) {
	cases := []struct {
		name  string
		fn    *ir.Func
		panic bool
	}{
		{"nil func", nil, false},
		{"not an intrinsic", &ir.Func{Name: "plain"}, false},
		{"bodyless with no emitter", &ir.Func{Name: "p", Intrinsic: "NoBackendHasThis"}, true},
		// A body is the declaration's claim that it computes the right answer,
		// and it is the whole claim: the two cases that used to pair it with a
		// `usable` flag said nothing this pair does not.
		{"a written body", &ir.Func{
			Name: "p", Intrinsic: "NoBackendHasThis",
			Block: []ir.Stmt{&ir.Return{}},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); (r != nil) != tc.panic {
					t.Errorf("panicked = %v; want %v (%v)", r != nil, tc.panic, r)
				}
			}()
			RequireIntrinsicFallback("some-lang", tc.fn)
		})
	}
}
