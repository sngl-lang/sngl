package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passHoistMutations = pass{
	name:    "HoistMutations",
	enabled: func(Caps) bool { return true },
	apply:   lowerHoistMutations,
}

// lowerHoistMutations makes a receiver-mutating intrinsic call -- `list.push`,
// `list.remove`, `remote.refresh` -- appear only as a statement, replacing it
// in every expression position with its receiver.
//
//	items = items.push("new")   ->  items.push("new")
//	other = items.push("new")   ->  items.push("new"); other = items
//	n = items.push("new").length()  ->  items.push("new"); n = items.length()
//
// The backends already assume this. `list.push` mutates its receiver and
// returns it, and no host language has one expression that does both: Go's
// emitter writes the whole statement `xs = append(xs, v)`, JavaScript's
// `xs.push(v)` answers the new length rather than the list, and Kotlin's
// `xs.add(v)` a Boolean. So an expression-position call produced Go that did
// not parse -- `m.items = m.items = append(m.items, "new")` -- and JavaScript
// and Kotlin that compiled and assigned the wrong value. internal/lower's own
// list_lambda has said so in a comment since it was written: it emits its
// pushes as a CallStmt "rather than wrapping the call in an Assign (which
// would double-emit the `=`)". This pass is that requirement made true for
// source the user wrote.
//
// It runs before passReactivity, so a mutation the pass now sees as a
// statement fires the receiver's updaters through mutatingCallReceiver. In
// `other = items.push(v)` it would otherwise have fired only `other`'s, and
// nothing reading `items` would have re-rendered.
func lowerHoistMutations(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &mutHoistState{}
	// imperativeBlocks rather than walkPackage: it reaches lambda bodies,
	// which is where a handler's statements live on a target whose primitives
	// declare their callback as a prop, and it leaves out view bodies, which
	// have no statement position to hoist into. A call left in one is what
	// verifyMutationsAreStatements reports.
	for _, block := range imperativeBlocks(pkg) {
		*block = st.transformBlock(*block)
	}
	if st.err != nil {
		return st.err
	}
	return verifyMutationsAreStatements(pkg)
}

type mutHoistState struct {
	err error
}

func (st *mutHoistState) transformBlock(block []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range block {
		pre, rewritten := st.transformStmt(s)
		out = append(out, pre...)
		if rewritten != nil {
			out = append(out, rewritten)
		}
	}
	return out
}

func (st *mutHoistState) transformStmt(s ir.Stmt) ([]ir.Stmt, ir.Stmt) {
	var pre []ir.Stmt
	switch n := s.(type) {
	case *ir.Assign:
		// `x = x.push(v)` is the whole statement already: push mutates the
		// receiver and returns it, so assigning the result back to it says
		// nothing the call has not done.
		if c, ok := n.Value.(*ir.Call); ok {
			if recv := mutatingCallReceiver(c); recv != nil && sameLValue(n.Target, recv) {
				return nil, &ir.CallStmt{AST: n.AST, Call: c}
			}
		}
		pre, n.Value = st.hoist(n.Value)
	case *ir.LocalVar:
		if n.Init != nil {
			pre, n.Init = st.hoist(n.Init)
		}
	case *ir.Return:
		if n.Value != nil {
			pre, n.Value = st.hoist(n.Value)
		}
	case *ir.CallStmt:
		// The call itself is already a statement; only its arguments can hold
		// another one.
		if n.Call != nil && mutatingCallReceiver(n.Call) == nil {
			pre = st.hoistArgs(n.Call)
		}
	case *ir.If:
		pre, n.Cond = st.hoist(n.Cond)
		n.Body = st.transformBlock(n.Body)
		n.Else = st.transformBlock(n.Else)
	case *ir.For:
		pre, n.Iter = st.hoist(n.Iter)
		n.Body = st.transformBlock(n.Body)
		n.Else = st.transformBlock(n.Else)
	case *ir.Emit:
		for i := range n.Args {
			p, v := st.hoist(n.Args[i].Value)
			pre = append(pre, p...)
			n.Args[i].Value = v
		}
	}
	return pre, s
}

func (st *mutHoistState) hoistArgs(c *ir.Call) []ir.Stmt {
	var pre []ir.Stmt
	for i := range c.Args {
		p, v := st.hoist(c.Args[i].Value)
		pre = append(pre, p...)
		c.Args[i].Value = v
	}
	return pre
}

// hoist rewrites e and returns the statements that must run before it.
func (st *mutHoistState) hoist(e ir.Expr) ([]ir.Stmt, ir.Expr) {
	if e == nil {
		return nil, nil
	}
	var pre []ir.Stmt
	// A call that is the whole expression has to be handled here: ir.Rewrite
	// walks from a root it cannot itself replace.
	if c, ok := e.(*ir.Call); ok {
		if recv := mutatingCallReceiver(c); recv != nil {
			pre = append(pre, st.hoistArgs(c)...)
			return append(pre, &ir.CallStmt{Call: c}), recv
		}
	}
	_ = ir.RewriteExprs(e, func(x ir.Expr) (ir.Expr, error) {
		switch n := x.(type) {
		case *ir.Lambda, *ir.Closure:
			// A lambda body is its own block, reached separately. Hoisting out
			// of one into the statement around the lambda would run the
			// mutation where the lambda is built rather than where it is
			// called.
			return x, ir.SkipDir
		case *ir.Ternary:
			p, cond := st.hoist(n.Cond)
			pre = append(pre, p...)
			n.Cond = cond
			st.refuseGuarded(n.Then, "a ternary arm")
			st.refuseGuarded(n.Else, "a ternary arm")
			return x, ir.SkipDir
		case *ir.Binary:
			if n.Op != ast.BinAnd && n.Op != ast.BinOr {
				return x, nil
			}
			p, left := st.hoist(n.Left)
			pre = append(pre, p...)
			n.Left = left
			st.refuseGuarded(n.Right, "the right operand of "+n.Op.String())
			return x, ir.SkipDir
		case *ir.Call:
			if recv := mutatingCallReceiver(n); recv != nil {
				pre = append(pre, &ir.CallStmt{Call: n})
				return recv, nil
			}
		}
		return x, nil
	})
	return pre, e
}

// refuseGuarded reports a mutation the hoist cannot move: one that runs only
// when the expression around it says so. Lifting it out would run it always.
func (st *mutHoistState) refuseGuarded(e ir.Expr, where string) {
	if st.err != nil || e == nil {
		return
	}
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		c, ok := x.(*ir.Call)
		if !ok || mutatingCallReceiver(c) == nil {
			return nil
		}
		st.err = fmt.Errorf("%s mutates its receiver, so its result cannot be used inside %s: bind it to a variable first",
			intrinsicName(c), where)
		return ir.SkipAll
	})
}

// verifyMutationsAreStatements is the backstop: every backend emits one of
// these as a statement, so one left anywhere else would be emitted as source
// that does not compile or that answers the wrong value. A view body is where
// this bites -- it holds no statements, so the hoist has nowhere to move one
// to and this is the only report the user gets.
func verifyMutationsAreStatements(pkg *ir.Package) error {
	stmtCalls := map[*ir.Call]bool{}
	_ = ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if cs, ok := s.(*ir.CallStmt); ok && cs.Call != nil {
			stmtCalls[cs.Call] = true
		}
		return nil
	})
	var err error
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		c, ok := e.(*ir.Call)
		if !ok || stmtCalls[c] || mutatingCallReceiver(c) == nil {
			return nil
		}
		err = fmt.Errorf("%s mutates its receiver and returns it, which no target can spell as an expression; write it as a statement of its own", intrinsicName(c))
		return ir.SkipAll
	})
	return err
}

func intrinsicName(c *ir.Call) string {
	if c == nil || c.Func == nil || c.Func.Intrinsic == "" {
		return "the call"
	}
	return c.Func.Intrinsic
}

// sameLValue reports whether two expressions denote the same storage. It is
// conservative: an unrecognised shape answers false, and the caller then emits
// the general two-statement form, which is correct either way.
func sameLValue(a, b ir.Expr) bool {
	switch x := a.(type) {
	case *ir.Ident:
		y, ok := b.(*ir.Ident)
		return ok && x.Sym != nil && x.Sym == y.Sym
	case *ir.Select:
		y, ok := b.(*ir.Select)
		return ok && x.Field == y.Field && sameLValue(x.Operand, y.Operand)
	}
	return false
}
