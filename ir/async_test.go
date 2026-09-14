package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A closure holding a blocking call does not make the expression holding the
// closure a blocking one: constructing it is not calling it, and the closure's
// own Func is coloured in its own right.
//
// It read the other way round until `time.timer` became an effect on the Go
// platforms. `handle = every(d, func(){ ...blocking... })` was then an async
// statement, and passAsyncOffload refuses one inside the `if` its caller wraps
// it in -- for a body that does not block at all.
func TestExprHasAsyncCall_LambdaBody(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	inner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
	}
	lam := &ir.Lambda{Func: inner}
	if ir.ExprHasAsyncCall(lam) {
		t.Fatalf("constructing a Lambda is not calling it")
	}
	if !ir.BlockHasAsyncCall(inner.Block) {
		t.Fatalf("the Lambda's own body is what blocks")
	}
}

func TestExprHasAsyncCall_ClosureBody(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	inner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
	}
	cl := &ir.Closure{Func: inner}
	if ir.ExprHasAsyncCall(cl) {
		t.Fatalf("constructing a Closure is not calling it")
	}

	nilFunc := &ir.Closure{Func: nil}
	if ir.ExprHasAsyncCall(nilFunc) {
		t.Fatalf("Closure with nil Func should report no async")
	}
}

func TestBlockHasAsyncCall_DirectCall(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	syncFn := &ir.Func{Name: "noop", IsAsync: false}

	block := []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}}
	if !ir.BlockHasAsyncCall(block) {
		t.Fatalf("expected async detected")
	}

	block2 := []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: syncFn}}}
	if ir.BlockHasAsyncCall(block2) {
		t.Fatalf("expected no async")
	}
}
