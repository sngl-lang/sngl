package optimize

import (
	"testing"

	"duckfam.us/sngl/ir"
)

// A copy of a guarded `if` owns its finally block, as it owns its body: a fold
// of one copy's finally -- an unrolled loop's, a document's own -- wrote
// through to every other copy's when the clone shared the slice.
func TestACloneOwnsItsFinally(t *testing.T) {
	flag := &ir.Var{Name: "done", Type: ir.TypBool}
	ref := &ir.Ident{Name: "done", Type: ir.TypBool, Sym: flag}
	orig := &ir.If{
		Cond:    &ir.Literal{Type: ir.TypBool, Value: "true"},
		Finally: []ir.Stmt{&ir.Assign{Target: ref, Value: &ir.Literal{Type: ir.TypBool, Value: "false"}}},
	}
	cp, ok := cloneStmts([]ir.Stmt{orig})[0].(*ir.If)
	if !ok || len(cp.Finally) != 1 {
		t.Fatalf("the clone's finally is %v", cp)
	}
	if cp.Finally[0] == orig.Finally[0] || &cp.Finally[0] == &orig.Finally[0] {
		t.Error("the clone shares its finally block with the original")
	}
}
