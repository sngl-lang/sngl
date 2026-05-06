package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestExprHasAsyncCall_LambdaBody(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	inner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
	}
	lam := &ir.Lambda{Func: inner}
	if !ir.ExprHasAsyncCall(lam) {
		t.Fatalf("expected async detected through Lambda.Func.Block")
	}

	syncInner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: &ir.Func{Name: "noop"}}}},
	}
	syncLam := &ir.Lambda{Func: syncInner}
	if ir.ExprHasAsyncCall(syncLam) {
		t.Fatalf("did not expect async in sync Lambda")
	}
}

func TestExprHasAsyncCall_ClosureBody(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	inner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
	}
	cl := &ir.Closure{Func: inner}
	if !ir.ExprHasAsyncCall(cl) {
		t.Fatalf("expected async detected through Closure.Func.Block")
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
