package kotlin

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The Kotlin forms of the list intrinsics whose edges the backends disagreed
// on (GitLab #100): subList returned a live view of the receiver and threw out
// of range, removeAt threw, where lib/builtin/methods.sngl documents a clamped
// copy and an out-of-range no-op.
//
// Unlike the Go and JS cases these are not executed — there is no Kotlin
// toolchain in this module, and an execution test that only ever skips asserts
// nothing. What is checked here is structural: the guard is present, and each
// operand is spelled once so an argument with a side effect runs once.

func emitKt(t *testing.T, id string, names []string, types []*ir.Type) string {
	t.Helper()
	fn := codegen.LookupIntrinsic(langKt, id)
	if fn == nil {
		t.Fatalf("%s: no Kotlin emitter", id)
	}
	args := make([]ir.Expr, len(names))
	for i, n := range names {
		args[i] = &ir.Ident{Name: n, Type: types[i]}
	}
	code, _ := fn(args, func(e ir.Expr) string { return e.(*ir.Ident).Name })
	return code
}

func TestKotlinListSliceClampsAndCopies(t *testing.T) {
	code := emitKt(t, "list.slice",
		[]string{"recvExpr", "loExpr", "hiExpr"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt, ir.TypInt})

	for _, want := range []string{
		"coerceIn(0, __n)",  // both bounds clamped to [0, length]
		"maxOf(__lo, __hi)", // an inverted range is empty, not an exception
		".toList()",         // a copy, not the live subList view
	} {
		if !strings.Contains(code, want) {
			t.Errorf("list.slice emitted %q, want it to contain %q", code, want)
		}
	}
	// Each operand is an arbitrary expression; spelling one twice would run it
	// twice.
	for _, operand := range []string{"recvExpr", "loExpr", "hiExpr"} {
		if n := strings.Count(code, operand); n != 1 {
			t.Errorf("list.slice spells %q %d times, want 1: %s", operand, n, code)
		}
	}
}

func TestKotlinListRemoveGuardsItsIndex(t *testing.T) {
	code := emitKt(t, "list.remove",
		[]string{"recvExpr", "idxExpr"},
		[]*ir.Type{ir.ListOf(ir.TypInt), ir.TypInt})

	if !strings.Contains(code, "__i >= 0 && __i < __l.size") {
		t.Errorf("list.remove emitted %q, want an in-range guard around removeAt", code)
	}
	if strings.HasPrefix(code, "recvExpr.removeAt(") {
		t.Errorf("list.remove is still the unguarded form: %s", code)
	}
	for _, operand := range []string{"recvExpr", "idxExpr"} {
		if n := strings.Count(code, operand); n != 1 {
			t.Errorf("list.remove spells %q %d times, want 1: %s", operand, n, code)
		}
	}
}
