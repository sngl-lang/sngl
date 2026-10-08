package ir_test

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// Constructing a closure is not calling it, so a closure holding a blocking
// call does not make the expression holding *the closure* a blocking one.
//
// The distinction is what "async" means to each reader. To JavaScript it is the
// `async` keyword, which belongs on the function whose body has an `await` in
// it -- the arrow, not whoever built the arrow and handed it to setInterval. To
// Go there is no keyword at all and the colour means "passAsyncOffload must
// give this body a goroutine", which is likewise the arrow: the builder hands
// work over and waits for nothing. Reading through the closure conflated the
// two, and the caller inherited a colour describing work it does not do.
//
// It read the other way round until `time.timer` became an effect on the Go
// platforms, where the consequence was not cosmetic:
// `handle = every(d, func(){ ...blocking... })` became an async statement, and
// passAsyncOffload refuses one inside an `if` -- which is where an effect's
// `_up` calls its mount from. A correct program stopped compiling.
//
// Nothing is lost by stopping here, because the closure's own Func is coloured
// in its own right: allFuncsAndLambdas lists every lambda, and recolourAsync
// walks for them again over what lowering synthesized. The descent was
// redundant as well as wrong.
//
// These two tests pin the rule but are not the coverage. A unit test over a
// hand-built IR node is what let the old behaviour sit here unquestioned: it
// says what the function returns and nothing about what a program compiles to.
// testdata/generate_async_timer.txtar is the claim on JavaScript (three
// `async` keywords that go away) and testdata/timer_tick_async_offload.txtar is
// the claim on both Go platforms. Kotlin reads IsAsync nowhere, so there is no
// third; a fixture for it would assert identical output either way.
func TestExprHasAsyncCall_LambdaBody(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	inner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
	}
	lam := &ir.Lambda{Func: inner}
	if ir.ExprHasAsyncCall(lam) {
		t.Fatalf("constructing a Lambda is not calling it")
	}
	// The other half of the same rule: the body is still what blocks, and
	// whoever colours the lambda itself asks about the body directly.
	if !ir.BlockHasAsyncCall(inner.Block) {
		t.Fatalf("the Lambda's own body is what blocks")
	}
}

// ir.Closure is the lifted form passLambda produces, and it answers the same
// way for the same reason -- with one difference worth keeping in mind: its
// Func is appended to pkg.Funcs, so the colouring fixpoint reaches it as an
// ordinary declaration rather than needing to walk for it.
func TestExprHasAsyncCall_ClosureBody(t *testing.T) {
	asyncFn := &ir.Func{Name: "fetchHello", IsAsync: true}
	inner := &ir.Func{
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Func: asyncFn}}},
	}
	cl := &ir.Closure{Func: inner}
	if ir.ExprHasAsyncCall(cl) {
		t.Fatalf("constructing a Closure is not calling it")
	}
	if !ir.BlockHasAsyncCall(inner.Block) {
		t.Fatalf("the Closure's own body is what blocks")
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
