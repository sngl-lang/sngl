package ir_test

import (
	"errors"
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
	ir.InspectPackage(pkg, ir.Inspector{
		Stmt: func(ir.Stmt) error { stmts++; return nil },
		Expr: func(ir.Expr) error { exprs++; return nil },
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
	ir.InspectPackage(pkg, ir.Inspector{
		Expr: func(ir.Expr) error { seen++; return ir.SkipAll }, // stop on first
	})
	if seen != 1 {
		t.Errorf("early-stop visited %d exprs, want 1", seen)
	}
}

func TestWalkNilPackage(t *testing.T) {
	// Must not panic.
	ir.InspectPackage(nil, ir.Inspector{Stmt: func(ir.Stmt) error { return nil }})
}

func TestInspectFuncSubtree(t *testing.T) {
	pkg := buildWalkPkg()
	fn := pkg.Funcs[0]

	var stmts, exprs int
	ir.InspectFunc(fn, ir.Inspector{
		Stmt: func(ir.Stmt) error { stmts++; return nil },
		Expr: func(ir.Expr) error { exprs++; return nil },
	})
	// Only the func body: If, Assign, CallStmt = 3 stmts. Exprs: If.Cond
	// Ident (1), Assign target Ident + value Literal (2), CallStmt.Call (1) = 4.
	// The package var's Binary is NOT reached (subtree root is the func).
	if stmts != 3 {
		t.Errorf("InspectFunc stmt visits = %d, want 3", stmts)
	}
	if exprs != 4 {
		t.Errorf("InspectFunc expr visits = %d, want 4", exprs)
	}
}

func TestInspectStmtsSubtree(t *testing.T) {
	pkg := buildWalkPkg()

	var stmts int
	ir.InspectStmts(pkg.Funcs[0].Block, ir.Inspector{
		Stmt: func(ir.Stmt) error { stmts++; return nil },
	})
	if stmts != 3 {
		t.Errorf("InspectStmts stmt visits = %d, want 3", stmts)
	}
}

func TestInspectSkipChildrenPrunesButContinuesSiblings(t *testing.T) {
	pkg := buildWalkPkg()

	// Prune the If (skip its nested Assign) but keep visiting siblings
	// (the CallStmt). Expect: If + CallStmt visited = 2, Assign skipped.
	var seen []string
	ir.InspectFunc(pkg.Funcs[0], ir.Inspector{
		Stmt: func(s ir.Stmt) error {
			switch s.(type) {
			case *ir.If:
				seen = append(seen, "if")
				return ir.SkipDir
			case *ir.Assign:
				seen = append(seen, "assign")
			case *ir.CallStmt:
				seen = append(seen, "call")
			}
			return nil
		},
	})
	if len(seen) != 2 || seen[0] != "if" || seen[1] != "call" {
		t.Errorf("SkipChildren visits = %v, want [if call] (Assign pruned, sibling kept)", seen)
	}
}

func TestInspectStopHaltsWholeWalk(t *testing.T) {
	pkg := buildWalkPkg()
	var seen int
	ir.InspectFunc(pkg.Funcs[0], ir.Inspector{
		Stmt: func(ir.Stmt) error { seen++; return ir.SkipAll },
	})
	if seen != 1 {
		t.Errorf("Stop visited %d stmts, want 1", seen)
	}
}

func TestInspectExprSubtree(t *testing.T) {
	// (1+2) * x : Binary(Mul, Binary(Add,1,2), Ident x). Prune the inner
	// Add subtree; expect to still visit the outer Binary, the inner Binary,
	// and the Ident sibling — but not the two Literals inside the pruned Add.
	inner := &ir.Binary{Left: &ir.Literal{Raw: "1"}, Right: &ir.Literal{Raw: "2"}}
	root := &ir.Binary{Left: inner, Right: &ir.Ident{Name: "x"}}
	var lits, idents, bins int
	ir.InspectExpr(root, ir.Inspector{
		Expr: func(e ir.Expr) error {
			switch e.(type) {
			case *ir.Literal:
				lits++
			case *ir.Ident:
				idents++
			case *ir.Binary:
				bins++
				if e == inner {
					return ir.SkipDir // prune the inner Add's literals
				}
			}
			return nil
		},
	})
	if bins != 2 || idents != 1 || lits != 0 {
		t.Errorf("InspectExpr prune: bins=%d idents=%d lits=%d, want 2/1/0", bins, idents, lits)
	}
}

func TestInspectBubblesRealError(t *testing.T) {
	pkg := buildWalkPkg()
	sentinel := errors.New("boom")
	got := ir.InspectPackage(pkg, ir.Inspector{
		Expr: func(ir.Expr) error { return sentinel },
	})
	if got != sentinel {
		t.Errorf("InspectPackage returned %v, want the callback's error", got)
	}
}

func TestInspectSkipAllSwallowed(t *testing.T) {
	pkg := buildWalkPkg()
	got := ir.InspectPackage(pkg, ir.Inspector{
		Expr: func(ir.Expr) error { return ir.SkipAll },
	})
	if got != nil {
		t.Errorf("InspectPackage returned %v, want nil (SkipAll swallowed)", got)
	}
}
