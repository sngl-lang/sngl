package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

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
