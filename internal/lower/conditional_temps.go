package lower

import (
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passConditionalTemps makes a statement of each owned value a statement may
// not make -- an arm of a ternary or of a match expression, the right of `&&`
// or `||`, the head of a condition `for` -- so that ReleaseTemps, which
// releases what a statement borrows, sees one. An arm becomes an assignment
// to a local under an `if`, which is NoTernary's shape, written here for the
// arms that allocate on every target: a target with ternaries keeps the rest.
// A condition head becomes a function called per iteration whose body returns
// it, so that what it allocates is a `return`'s temp, released before the
// test. An iterable head is ReleaseTemps's own: evaluated once, it is bound
// before the loop and released after it.
var passConditionalTemps = pass{
	name:    "ConditionalTemps",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, _ Options) error {
		st := &conditionalTemps{}
		for _, block := range append(imperativeBlocks(pkg), drawBlocks(pkg)...) {
			st.block(block)
		}
		return nil
	},
}

// ConditionalTemps is passConditionalTemps for the interpreter, over every
// body the program reaches, as ReleaseTemps is.
func ConditionalTemps(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := &conditionalTemps{}
	for _, block := range reachedBlocks(pkg) {
		st.block(block)
	}
	return nil
}

type conditionalTemps struct {
	counter int
}

func (st *conditionalTemps) block(block *[]ir.Stmt) {
	*block = ir.RewriteBlocks(*block, st.flat)
}

func (st *conditionalTemps) flat(stmts []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		out = append(out, st.stmt(s)...)
	}
	return out
}

// stmt is s with each conditional value it evaluates computed by a statement
// before it. The kinds that carry an expression are ReleaseTemps's; the lists
// a statement holds have already been done.
func (st *conditionalTemps) stmt(s ir.Stmt) []ir.Stmt {
	var pre []ir.Stmt
	switch n := s.(type) {
	case *ir.CallStmt:
		if n.Call != nil {
			var e ir.Expr = n.Call
			pre, e = st.expr(e)
			n.Call = e.(*ir.Call)
		}
	case *ir.Assign:
		pre, n.Value = st.expr(n.Value)
	case *ir.LocalVar:
		if n.Init != nil {
			pre, n.Init = st.expr(n.Init)
		}
	case *ir.Return:
		if n.Value != nil {
			pre, n.Value = st.expr(n.Value)
		}
	case *ir.If:
		if n.Catch == nil {
			pre, n.Cond = st.expr(n.Cond)
		}
	case *ir.Match:
		if n.Subject != nil {
			pre, n.Subject = st.expr(n.Subject)
		}
	case *ir.For:
		pre, n.Iter = st.head(n)
	}
	return append(pre, s)
}

func (st *conditionalTemps) head(n *ir.For) ([]ir.Stmt, ir.Expr) {
	if n.Iter == nil {
		return nil, nil
	}
	if ir.DeriveIterKind(n) == ir.IterCondition && allocates(n.Iter) {
		fn := &ir.Func{Return: ir.TypBool, Block: st.flat([]ir.Stmt{&ir.Return{Value: n.Iter}})}
		return nil, &ir.Call{Type: ir.TypBool, Callee: &ir.Lambda{Type: ir.FuncOf(nil, ir.TypBool), Func: fn}}
	}
	return st.expr(n.Iter)
}

// expr is e with each site whose arm allocates replaced by a read of the local
// the statements it answers compute, in the order the sites are evaluated: one
// in another's condition is computed before it, one in its arm inside the arm.
// A lambda's body is a block of its own, reached on its own.
func (st *conditionalTemps) expr(e ir.Expr) ([]ir.Stmt, ir.Expr) {
	var pre []ir.Stmt
	out, _ := ir.RewriteExpr(e, func(n ir.Node) (ir.Node, error) {
		var p []ir.Stmt
		var repl ir.Expr
		switch x := n.(type) {
		case ir.Stmt, *ir.Lambda:
			return n, ir.SkipDir
		case *ir.Ternary:
			if allocates(x.Then) || allocates(x.Else) {
				p, repl = st.ternary(x)
			}
		case *ir.MatchExpr:
			if slices.ContainsFunc(x.Arms, func(a *ir.MatchArm) bool { return allocates(a.Value) }) {
				p, repl = st.match(x)
			}
		case *ir.Binary:
			if (x.Op == ast.BinAnd || x.Op == ast.BinOr) && allocates(x.Right) {
				p, repl = st.shortCircuit(x)
			}
		}
		if repl == nil {
			return n, nil
		}
		pre = append(pre, p...)
		return repl, ir.SkipDir
	})
	return pre, out
}

func (st *conditionalTemps) local(typ *ir.Type) *ir.Var {
	v := &ir.Var{Name: "__arm" + strconv.Itoa(st.counter), Type: typ, Synthesized: true}
	st.counter++
	return v
}

func assignTo(v *ir.Var, value ir.Expr) ir.Stmt {
	return &ir.Assign{Target: identOf(v), Op: ast.AssignSet, Value: value}
}

func (st *conditionalTemps) ternary(t *ir.Ternary) ([]ir.Stmt, ir.Expr) {
	condPre, cond := st.expr(t.Cond)
	v := st.local(t.Type)
	thenPre, then := st.expr(t.Then)
	elsePre, els := st.expr(t.Else)
	pre := append(condPre, &ir.LocalVar{Name: v.Name, Type: v.Type, Sym: v}, &ir.If{
		Cond:        cond,
		Body:        append(thenPre, assignTo(v, then)),
		Else:        append(elsePre, assignTo(v, els)),
		FromTernary: true,
	})
	return pre, identOf(v)
}

func (st *conditionalTemps) match(m *ir.MatchExpr) ([]ir.Stmt, ir.Expr) {
	var pre []ir.Stmt
	if m.Subject != nil {
		pre, m.Subject = st.expr(m.Subject)
	}
	v := st.local(m.Type)
	stmt := &ir.Match{Subject: m.Subject, Exhaustive: m.Exhaustive, FromValue: true}
	if m.AST != nil {
		stmt.AST = &ast.MatchStmt{Pos: m.AST.Pos}
	}
	for _, arm := range m.Arms {
		valPre, val := st.expr(arm.Value)
		stmt.Arms = append(stmt.Arms, &ir.MatchArm{
			Pos:      arm.Pos,
			Patterns: arm.Patterns,
			Else:     arm.Else,
			Body:     append(valPre, assignTo(v, val)),
		})
	}
	return append(pre, &ir.LocalVar{Name: v.Name, Type: v.Type, Sym: v}, stmt), identOf(v)
}

// shortCircuit is `a && b` as `var v = a; if v { v = b }`, and `a || b` with
// the test negated.
func (st *conditionalTemps) shortCircuit(b *ir.Binary) ([]ir.Stmt, ir.Expr) {
	leftPre, left := st.expr(b.Left)
	v := st.local(b.Type)
	rightPre, right := st.expr(b.Right)
	var test ir.Expr = identOf(v)
	if b.Op == ast.BinOr {
		test = &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: test}
	}
	pre := append(leftPre, &ir.LocalVar{Name: v.Name, Type: v.Type, Init: left, Sym: v}, &ir.If{
		Cond:        test,
		Body:        append(rightPre, assignTo(v, right)),
		FromTernary: true,
	})
	return pre, identOf(v)
}

// allocates reports whether evaluating e makes an owned value somewhere in
// it. A lambda's body runs when it is called.
func allocates(e ir.Expr) bool {
	found := false
	_ = ir.Walk(e, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Lambda:
			return ir.SkipDir
		case ir.Expr:
			if isAllocation(x) {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	return found
}
