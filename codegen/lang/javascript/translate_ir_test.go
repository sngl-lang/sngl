package javascript

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

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
