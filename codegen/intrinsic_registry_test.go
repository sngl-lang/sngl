package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIntrinsicRegistry(t *testing.T) {
	const lang = "test-lang-reg"
	RegisterIntrinsic(lang, "Doubler", func(args []ir.Expr, tr func(ir.Expr) string) string {
		return tr(args[0]) + " * 2"
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
	out, ok := EmitIntrinsicCall(lang, call, func(e ir.Expr) string {
		return e.(*ir.Ident).Name
	})
	if !ok || out != "x * 2" {
		t.Errorf("EmitIntrinsicCall = (%q, %v); want (\"x * 2\", true)", out, ok)
	}

	// A call with no intrinsic, or an unknown lang, declines.
	if _, ok := EmitIntrinsicCall(lang, &ir.Call{Func: &ir.Func{}}, nil); ok {
		t.Error("call without an intrinsic ID must decline")
	}
	if _, ok := EmitIntrinsicCall("other-lang", call, func(e ir.Expr) string { return "" }); ok {
		t.Error("unknown lang must decline")
	}
}

func TestIntrinsicRegistryDuplicatePanics(t *testing.T) {
	const lang = "test-lang-dup"
	RegisterIntrinsic(lang, "X", func([]ir.Expr, func(ir.Expr) string) string { return "" })
	defer func() {
		if recover() == nil {
			t.Error("duplicate registration did not panic")
		}
	}()
	RegisterIntrinsic(lang, "X", func([]ir.Expr, func(ir.Expr) string) string { return "" })
}
