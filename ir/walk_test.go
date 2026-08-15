package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// buildWalkPkg constructs a small package exercising nested statements and
// expressions: a package var with a binary init, and a func whose body has
// an If (with a nested Assign) and a CallStmt.
func buildWalkPkg() *ir.Package {
	v := &ir.Var{
		Name: "x",
		Init: &ir.Binary{Left: &ir.Literal{Raw: "1"}, Right: &ir.Literal{Raw: "2"}},
	}
	fn := &ir.Func{
		Name: "f",
		Block: []ir.Stmt{
			&ir.If{
				Cond: &ir.Ident{Name: "cond"},
				Body: []ir.Stmt{
					&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Raw: "3"}},
				},
			},
			&ir.CallStmt{Call: &ir.Call{Func: &ir.Func{Name: "g"}}},
		},
	}
	return &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{fn}}
}

func TestWalkVisitsStmtsAndExprs(t *testing.T) {
	pkg := buildWalkPkg()

	var stmts, exprs int
	ir.Walk(pkg, ir.VisitorFuncs{
		Stmt: func(ir.Stmt) bool { stmts++; return false },
		Expr: func(ir.Expr) bool { exprs++; return false },
	})

	// Statements: If, Assign, CallStmt = 3.
	if stmts != 3 {
		t.Errorf("stmt visits = %d, want 3", stmts)
	}
	// Expressions: Binary + its two Literals (3), If.Cond Ident (1),
	// Assign target Ident + value Literal (2), CallStmt.Call fed through
	// exprFn (1) = 7.
	if exprs != 7 {
		t.Errorf("expr visits = %d, want 7", exprs)
	}
}

func TestWalkEarlyStop(t *testing.T) {
	pkg := buildWalkPkg()

	var seen int
	ir.Walk(pkg, ir.VisitorFuncs{
		Expr: func(ir.Expr) bool { seen++; return true }, // stop on first
	})
	if seen != 1 {
		t.Errorf("early-stop visited %d exprs, want 1", seen)
	}
}

func TestWalkNilPackage(t *testing.T) {
	// Must not panic.
	ir.Walk(nil, ir.VisitorFuncs{Stmt: func(ir.Stmt) bool { return false }})
}
