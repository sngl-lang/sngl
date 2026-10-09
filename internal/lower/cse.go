package lower

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ir"
)

// passCSE binds a pure call that a statement makes more than once to a local,
// so it is made once.
//
// The duplicates are mostly not the author's. A pass that substitutes an
// argument into a body puts a copy at every use, so one written call becomes
// several; a widget that reads four channels off a color reads the expression
// that produced it four times. Unrolling used to hide this -- each copy folded
// against its own iteration -- and a target that emits the loop instead does
// the call once per channel per iteration.
//
// It is deliberately statement-local, and only over imperative blocks:
//
//   - Statement-local, because hoisting out of a loop is a different
//     transformation with a different proof: an expression over the loop
//     variable is not invariant, and one that is has to be shown so. Here the
//     temp lands in the same block, so the only claim being made is that the
//     value does not change between two points in one statement.
//
//   - Imperative blocks only -- function, handler and timer bodies -- because
//     that is where every target can hold a statement. A view body cannot on
//     a target with no host language: static html writes markup, and a temp
//     declared beside a node is not something markup can express, so the prop
//     reading it would render as nothing.
//
// Both bounds are what make the safety argument short. Within one statement no
// variable changes value (SNGL has no assignment expression; the target of an
// assignment is written after its value), so the only way a hoist could move a
// read across a write is an impure call in the same statement -- and a
// statement holding one is left alone.
//
// A call is never hoisted *out of* a lambda, for the same reason it is never
// hoisted out of a loop: the body runs later, or not at all, so a temp bound
// outside it is bound at a different time. A lambda's own body is a different
// question -- it is an imperative block like any other, and one statement in
// it making the same call twice makes it twice -- so imperativeBlocks hands
// each over as its own block and each is rewritten in place.
//
// That is also the only way the pass reaches an android handler at all; see
// imperativeBlocks for why.
var passCSE = pass{
	name:    "CSE",
	enabled: func(Features) bool { return true },
	apply:   lowerCSE,
}

func lowerCSE(pkg *ir.Package, _ Features, _ Options) error {
	st := &cseState{}
	for _, block := range imperativeBlocks(pkg) {
		st.imperative(block)
	}
	return nil
}

type cseState struct {
	counter int
}

// imperative rewrites one block in place, recursing into the blocks it holds.
func (st *cseState) imperative(block *[]ir.Stmt) {
	out := make([]ir.Stmt, 0, len(*block))
	for _, s := range *block {
		switch n := s.(type) {
		case *ir.If:
			st.imperative(&n.Body)
			st.imperative(&n.Else)
		case *ir.For:
			st.imperative(&n.Body)
			st.imperative(&n.Else)
		}
		out = append(out, st.hoist(s)...)
		out = append(out, s)
	}
	*block = out
}

// hoist returns the temps to declare before s: one per pure call s makes more
// than once. The occurrences are replaced with reads of the temp.
func (st *cseState) hoist(s ir.Stmt) []ir.Stmt {
	groups, ok := repeatedPureCalls(s)
	if !ok || len(groups) == 0 {
		return nil
	}
	var pre []ir.Stmt
	for _, g := range groups {
		name := "__cse" + strconv.Itoa(st.counter)
		st.counter++
		typ := g[0].ExprType()
		sym := &ir.Var{Name: name, Type: typ, Synthesized: true}
		for _, call := range g {
			replaceExpr(s, call, &ir.Ident{Name: name, Sym: sym, Type: typ})
		}
		pre = append(pre, &ir.LocalVar{Name: name, Type: typ, Init: g[0], Sym: sym})
	}
	return pre
}

// repeatedPureCalls groups the calls s makes by what they compute, keeping
// only the groups it makes more than once, in the order they were found.
// Reports false when s holds a call that may not be moved -- an impure one, or
// one whose purity is not known.
func repeatedPureCalls(s ir.Stmt) ([][]*ir.Call, bool) {
	order := []string{}
	byKey := map[string][]*ir.Call{}
	pure := true
	visitStmtCalls(s, func(c *ir.Call) {
		if !purelyRepeatable(c) {
			pure = false
			return
		}
		key, ok := exprKey(c)
		if !ok {
			return
		}
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], c)
	})
	if !pure {
		return nil, false
	}
	var groups [][]*ir.Call
	for _, key := range order {
		if g := byKey[key]; len(g) > 1 {
			groups = append(groups, g)
		}
	}
	return groups, true
}

// purelyRepeatable reports whether a call can be made once and read twice: it
// computes a value, mutates nothing, and its result is worth a name.
func purelyRepeatable(c *ir.Call) bool {
	if c == nil || c.Func == nil || c.Func.Purity != ir.PurityPure {
		return false
	}
	if c.ErrorMode != ir.ErrorNone {
		return false
	}
	if def := ir.LookupIntrinsic(c.Func.Intrinsic); def != nil && def.MutatesReceiver {
		return false
	}
	return c.ExprType() != nil && c.ExprType().Kind != ir.TypeVoid
}

// exprKey is what two expressions computing the same value share. Nil for an
// expression whose equality this cannot decide, which keeps it out of every
// group rather than merging it with something else.
func exprKey(e ir.Expr) (string, bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type == nil {
			return "", false
		}
		return "lit(" + x.Type.String() + "," + x.Value + x.Suffix + ")", true
	case *ir.Ident:
		if x.Sym == nil {
			return "", false
		}
		// The symbol, not the name: two names may be one binding, and one
		// name in two scopes is two.
		return "id(" + identityOf(x.Sym) + ")", true
	case *ir.Select:
		operand, ok := exprKey(x.Operand)
		if !ok {
			return "", false
		}
		return "sel(" + operand + "." + x.Field + ")", true
	case *ir.Binary:
		lhs, lok := exprKey(x.Left)
		rhs, rok := exprKey(x.Right)
		if !lok || !rok {
			return "", false
		}
		return "bin(" + x.Op.String() + "," + lhs + "," + rhs + ")", true
	case *ir.Call:
		if x.Func == nil {
			return "", false
		}
		var key strings.Builder
		key.WriteString("call(" + identityOf(x.Func))
		if x.Receiver != nil {
			recv, ok := exprKey(x.Receiver)
			if !ok {
				return "", false
			}
			key.WriteString(",recv=" + recv)
		}
		for _, a := range x.Args {
			arg, ok := exprKey(a.Value)
			if !ok {
				return "", false
			}
			key.WriteString("," + a.Name + "=" + arg)
		}
		return key.String() + ")", true
	}
	return "", false
}

// identityOf names a declaration by its address, so two references to one
// declaration key alike and two declarations sharing a name do not.
func identityOf(sym any) string { return fmt.Sprintf("%p", sym) }

// visitStmtCalls calls fn for every Call in the expressions s owns.
//
// Owns is the operative word: a nested block is a statement of its own and is
// visited when the block is, and a lambda body is not visited at all -- it
// runs when something calls it, which is not here.
func visitStmtCalls(s ir.Stmt, fn func(*ir.Call)) {
	for _, e := range stmtOwnExprs(s) {
		visitExprCalls(e, fn)
	}
}

func visitExprCalls(e ir.Expr, fn func(*ir.Call)) {
	switch x := e.(type) {
	case nil, *ir.Lambda:
		return
	case *ir.Call:
		fn(x)
		visitExprCalls(x.Receiver, fn)
		for _, a := range x.Args {
			visitExprCalls(a.Value, fn)
		}
	case *ir.Binary:
		visitExprCalls(x.Left, fn)
		visitExprCalls(x.Right, fn)
	case *ir.Unary:
		visitExprCalls(x.Operand, fn)
	case *ir.Ternary:
		visitExprCalls(x.Cond, fn)
		visitExprCalls(x.Then, fn)
		visitExprCalls(x.Else, fn)
	case *ir.Select:
		visitExprCalls(x.Operand, fn)
	case *ir.Index:
		visitExprCalls(x.Operand, fn)
		visitExprCalls(x.Idx, fn)
	case *ir.Conversion:
		visitExprCalls(x.Operand, fn)
	case *ir.ListLit:
		for _, el := range x.Elems {
			visitExprCalls(el, fn)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			visitExprCalls(f.Value, fn)
		}
	case *ir.MapLitIR:
		for _, en := range x.Entries {
			visitExprCalls(en.Key, fn)
			visitExprCalls(en.Value, fn)
		}
	case *ir.Spread:
		visitExprCalls(x.Operand, fn)
	}
}

// stmtOwnExprs is the expressions a statement evaluates itself.
func stmtOwnExprs(s ir.Stmt) []ir.Expr {
	switch n := s.(type) {
	case *ir.Assign:
		return []ir.Expr{n.Value}
	case *ir.LocalVar:
		return []ir.Expr{n.Init}
	case *ir.Return:
		return []ir.Expr{n.Value}
	case *ir.CallStmt:
		if n.Call == nil {
			return nil
		}
		return []ir.Expr{n.Call}
	case *ir.If:
		return []ir.Expr{n.Cond}
	case *ir.For:
		return []ir.Expr{n.Iter}
	case *ir.Emit:
		out := make([]ir.Expr, 0, len(n.Args))
		for _, a := range n.Args {
			out = append(out, a.Value)
		}
		return out
	}
	return nil
}

// replaceExpr swaps one expression node for another wherever s holds it,
// matched by identity: two calls computing the same value are still two
// nodes, and each has to be rewritten in its own place.
func replaceExpr(s ir.Stmt, old, new ir.Expr) {
	_ = ir.RewriteExprs(s, func(e ir.Expr) (ir.Expr, error) {
		if e == old {
			return new, nil
		}
		return e, nil
	})
}
