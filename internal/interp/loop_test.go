package interp

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// forever is `for { }` with the body stmts given: no iterable at all.
func forever(body ...ir.Stmt) *ir.For {
	return &ir.For{
		AST:  &ast.ForStmt{Pos: ast.Pos{File: "loop.sngl", Line: 3, Column: 5}},
		Body: body,
	}
}

// TestForeverLoopIsBounded covers the iteration budget. `sngl test` runs its
// fixtures in-process, so a loop that never terminates hangs the whole suite
// with nothing saying which fixture did it: the budget makes that an error
// naming the loop instead. The bound is set small here because reaching the
// real one would run ten million iterations.
func TestForeverLoopIsBounded(t *testing.T) {
	env := NewEnv()
	env.maxIterations = 5

	err := env.Exec(forever())
	if err == nil {
		t.Fatal("Exec(for { }) = nil; want the iteration budget to report it")
	}
	if !strings.Contains(err.Error(), "without terminating") {
		t.Errorf("error = %q; want it to say the loop did not terminate", err)
	}
	// Positioned, so the message names the loop that ran away.
	if !strings.Contains(err.Error(), "loop.sngl:3:5") {
		t.Errorf("error = %q; want the loop's position in it", err)
	}
}

// A loop that leaves through its body is not bounded by anything: break and
// return both end it well inside the budget.
func TestForeverLoopEndsOnBreakAndReturn(t *testing.T) {
	env := NewEnv()
	env.maxIterations = 5

	if err := env.Exec(forever(&ir.Break{})); err != nil {
		t.Errorf("Exec(for { break }) = %v; want nil", err)
	}

	err := env.Exec(forever(&ir.Return{}))
	if !IsReturn(err) {
		t.Errorf("Exec(for { return }) = %v; want the return to travel out to its function body", err)
	}
}

// A `continue` ends the iteration, not the loop -- so a loop whose body only
// continues still runs out its budget rather than terminating.
func TestContinueDoesNotEndTheLoop(t *testing.T) {
	env := NewEnv()
	env.maxIterations = 5

	err := env.Exec(forever(&ir.Continue{}))
	if err == nil || !strings.Contains(err.Error(), "without terminating") {
		t.Errorf("Exec(for { continue }) = %v; want the iteration budget to report it", err)
	}
}
