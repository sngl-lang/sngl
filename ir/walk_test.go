package ir_test

import (
	"errors"
	"testing"

	"duckfam.us/sngl/ir"
)

// buildWalkPkg constructs a small package exercising nested statements and
// expressions: a package var with a binary init, and a func whose body has
// an If (with a nested Assign) and a CallStmt.
func buildWalkPkg() *ir.Package {
	v := &ir.Var{
		Name: "x",
		Init: &ir.Binary{Left: &ir.Literal{Value: "1"}, Right: &ir.Literal{Value: "2"}},
	}
	fn := &ir.Func{
		Name: "f",
		Block: []ir.Stmt{
			&ir.If{
				Cond: &ir.Ident{Name: "cond"},
				Body: []ir.Stmt{
					&ir.Assign{Target: &ir.Ident{Name: "x"}, Value: &ir.Literal{Value: "3"}},
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
	ir.Walk(pkg, func(n ir.Node) error {
		switch n.(type) {
		case ir.Stmt:
			stmts++
		case ir.Expr:
			exprs++
		}
		return nil
	})

	// Statements: If, Assign, CallStmt = 3.
	if stmts != 3 {
		t.Errorf("stmt visits = %d, want 3", stmts)
	}
	// Expressions: Binary + its two Literals (3), If.Cond Ident (1),
	// Assign target Ident + value Literal (2), CallStmt.Call (1) = 7.
	if exprs != 7 {
		t.Errorf("expr visits = %d, want 7", exprs)
	}
}

func TestWalkEarlyStop(t *testing.T) {
	pkg := buildWalkPkg()

	var seen int
	ir.WalkExprs(pkg, func(ir.Expr) error { seen++; return ir.SkipAll }) // stop on first
	if seen != 1 {
		t.Errorf("early-stop visited %d exprs, want 1", seen)
	}
}

func TestWalkNilRoot(t *testing.T) {
	// Must not panic.
	if err := ir.Walk(nil, func(ir.Node) error { return nil }); err != nil {
		t.Errorf("Walk(nil) = %v, want nil", err)
	}
}

func TestWalkFuncSubtree(t *testing.T) {
	pkg := buildWalkPkg()
	fn := pkg.Funcs[0]

	var stmts, exprs int
	ir.Walk(fn, func(n ir.Node) error {
		switch n.(type) {
		case ir.Stmt:
			stmts++
		case ir.Expr:
			exprs++
		}
		return nil
	})
	// Only the func body: If, Assign, CallStmt = 3 stmts. Exprs: If.Cond
	// Ident (1), Assign target Ident + value Literal (2), CallStmt.Call (1) = 4.
	// The package var's Binary is NOT reached (subtree root is the func).
	if stmts != 3 {
		t.Errorf("func-subtree stmt visits = %d, want 3", stmts)
	}
	if exprs != 4 {
		t.Errorf("func-subtree expr visits = %d, want 4", exprs)
	}
}

func TestWalkStmtsSubtree(t *testing.T) {
	pkg := buildWalkPkg()

	var stmts int
	ir.WalkStmts(pkg.Funcs[0].Block, func(ir.Stmt) error { stmts++; return nil })
	if stmts != 3 {
		t.Errorf("WalkStmts stmt visits = %d, want 3", stmts)
	}
}

func TestWalkSkipDirPrunesButContinuesSiblings(t *testing.T) {
	pkg := buildWalkPkg()

	// Prune the If (skip its nested Assign) but keep visiting siblings
	// (the CallStmt). Expect: If + CallStmt visited = 2, Assign skipped.
	var seen []string
	ir.WalkStmts(pkg.Funcs[0], func(s ir.Stmt) error {
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
	})
	if len(seen) != 2 || seen[0] != "if" || seen[1] != "call" {
		t.Errorf("SkipDir visits = %v, want [if call] (Assign pruned, sibling kept)", seen)
	}
}

func TestWalkSkipAllHaltsWholeWalk(t *testing.T) {
	pkg := buildWalkPkg()
	var seen int
	ir.WalkStmts(pkg.Funcs[0], func(ir.Stmt) error { seen++; return ir.SkipAll })
	if seen != 1 {
		t.Errorf("SkipAll visited %d stmts, want 1", seen)
	}
}

func TestWalkExprsSubtreePrune(t *testing.T) {
	// (1+2) * x : Binary(Mul, Binary(Add,1,2), Ident x). Prune the inner
	// Add subtree; expect to still visit the outer Binary, the inner Binary,
	// and the Ident sibling — but not the two Literals inside the pruned Add.
	inner := &ir.Binary{Left: &ir.Literal{Value: "1"}, Right: &ir.Literal{Value: "2"}}
	root := &ir.Binary{Left: inner, Right: &ir.Ident{Name: "x"}}
	var lits, idents, bins int
	ir.WalkExprs(root, func(e ir.Expr) error {
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
	})
	if bins != 2 || idents != 1 || lits != 0 {
		t.Errorf("WalkExprs prune: bins=%d idents=%d lits=%d, want 2/1/0", bins, idents, lits)
	}
}

func TestWalkBubblesRealError(t *testing.T) {
	pkg := buildWalkPkg()
	sentinel := errors.New("boom")
	got := ir.WalkExprs(pkg, func(ir.Expr) error { return sentinel })
	if got != sentinel {
		t.Errorf("Walk returned %v, want the callback's error", got)
	}
}

func TestWalkSkipAllSwallowed(t *testing.T) {
	pkg := buildWalkPkg()
	got := ir.WalkExprs(pkg, func(ir.Expr) error { return ir.SkipAll })
	if got != nil {
		t.Errorf("Walk returned %v, want nil (SkipAll swallowed)", got)
	}
}

// TestRewriteReplacesExprInPlace verifies the base engine writes a replacement
// back into its parent slot: every Ident "a" becomes Ident "b".
func TestRewriteReplacesExprInPlace(t *testing.T) {
	root := &ir.Binary{
		Left:  &ir.Ident{Name: "a"},
		Right: &ir.Binary{Left: &ir.Ident{Name: "a"}, Right: &ir.Literal{Value: "1"}},
	}
	err := ir.RewriteExprs(root, func(e ir.Expr) (ir.Expr, error) {
		if id, ok := e.(*ir.Ident); ok && id.Name == "a" {
			return &ir.Ident{Name: "b"}, nil
		}
		return e, nil
	})
	if err != nil {
		t.Fatalf("RewriteExprs: %v", err)
	}
	if l := root.Left.(*ir.Ident); l.Name != "b" {
		t.Errorf("root.Left = %q, want b", l.Name)
	}
	if r := root.Right.(*ir.Binary).Left.(*ir.Ident); r.Name != "b" {
		t.Errorf("nested Left = %q, want b", r.Name)
	}
}
