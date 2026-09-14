package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passIndexedIter rewrites a two-variable loop over a pull sequence into a
// one-variable loop with its own counter.
//
// `for var i, x = xs` over a list is a construct every backend already has:
// Go's range, JS's entries(), Kotlin's withIndex(). Over an iter<T> it is
// not. An iter<T> is a sequence something pulls from -- a range func in Go, a
// generator in JS -- and neither hands out an ordinal; Go rejects the second
// variable outright. Building the sequence into a list to get one is the
// allocation sngl:seq exists to avoid, so the ordinal becomes what it always
// was, a counter beside the loop:
//
//	var __iterIdx0 = -1
//	for var x = xs {
//	    __iterIdx0 = __iterIdx0 + 1
//	    …
//	}
//
// It counts from -1 and increments at the top of the body rather than at the
// bottom, so a `return` out of the middle of an iteration cannot leave the
// count behind.
//
// A sequence written in the loop head never arrives here: ir.CountedSeq
// answers both variable forms arithmetically, and IterCounted emits the
// ordinal as a second counter in the loop head itself. This is the held and
// the passed case, where nothing is known about the bounds.
var passIndexedIter = pass{
	name:    "IndexedIter",
	enabled: func(Caps) bool { return true },
	apply:   lowerIndexedIter,
}

func lowerIndexedIter(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &indexedIterState{}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return st.block(stmts) },
		expr:  func(e ir.Expr) ir.Expr { return e },
	})
	// A lambda's body is a block of its own, and walkPackage hands over no
	// expression's insides -- so a loop written there was left with its second
	// variable, which Go rejects over a range func. fyne's and gtk4's
	// `time.timer` put a tick in one, handed to the host scheduler, and that
	// is what reached it. Collected separately rather than descended into
	// above, so each block is rewritten exactly once however the two nest.
	for _, fn := range ownedLambdaFuncs(pkg) {
		fn.Block = st.block(fn.Block)
	}
	return nil
}

// ownedLambdaFuncs is the func behind every ir.Lambda in pkg.
//
// ir.Closure is deliberately absent: passLambda lifts one into pkg.Funcs, so
// walkPackage already hands its block over and collecting it here would
// rewrite the same block twice.
func ownedLambdaFuncs(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
			out = append(out, l.Func)
		}
		return nil
	})
	return out
}

type indexedIterState struct {
	counter int
}

func (st *indexedIterState) block(block []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(block))
	for _, s := range block {
		pre, rewritten := st.stmt(s)
		out = append(out, pre...)
		out = append(out, rewritten)
	}
	return out
}

// stmt returns the statements to emit before s, and s itself.
func (st *indexedIterState) stmt(s ir.Stmt) (pre []ir.Stmt, out ir.Stmt) {
	switch n := s.(type) {
	case *ir.For:
		n.Body = st.block(n.Body)
		n.Else = st.block(n.Else)
		return st.rewriteFor(n), n
	case *ir.If:
		n.Body = st.block(n.Body)
		n.Else = st.block(n.Else)
	case *ir.NodeInst:
		n.Children = st.block(n.Children)
		for _, h := range n.Handlers {
			if h.Func != nil {
				h.Func.Block = st.block(h.Func.Block)
			}
		}
	case *ir.SlotInst:
		n.Children = st.block(n.Children)
	case *ir.ErrorBoundary:
		n.Children = st.block(n.Children)
	case *ir.ContextProvider:
		n.Children = st.block(n.Children)
	case *ir.Window:
		n.Body = st.block(n.Body)
	}
	return nil, s
}

// rewriteFor turns n into a one-variable loop and returns the counter
// declaration to place before it, or nil when n is not an indexed loop over a
// pull sequence.
func (st *indexedIterState) rewriteFor(n *ir.For) []ir.Stmt {
	if n.Value == "" || n.Iter == nil || n.KeySym == nil {
		return nil
	}
	if t := n.Iter.ExprType(); t == nil || t.Kind != ir.TypeIter {
		return nil
	}
	if ir.CountedSeq(n) != nil {
		return nil
	}
	name := "__iterIdx" + strconv.Itoa(st.counter)
	st.counter++
	idx := &ir.Var{Name: name, Type: ir.TypInt, Synthesized: true}
	// The body's `i` resolves to the loop's key symbol; it is now an ordinary
	// local, so every reference has to point at the new one.
	rewriteIdentSym(n.Body, n.KeySym, idx)
	rewriteIdentSym(n.Else, n.KeySym, idx)
	n.Body = append([]ir.Stmt{&ir.Assign{
		Target: &ir.Ident{Name: name, Sym: idx, Type: ir.TypInt},
		Op:     ast.AssignAdd,
		Value:  &ir.Literal{Type: ir.TypInt, Value: "1"},
	}}, n.Body...)
	n.Key, n.KeySym = n.Value, n.ValueSym
	n.Value, n.ValueSym = "", nil
	return []ir.Stmt{&ir.LocalVar{
		Name: name,
		Type: ir.TypInt,
		Init: &ir.Unary{Op: ast.UnaryNeg, Operand: &ir.Literal{Type: ir.TypInt, Value: "1"}, Type: ir.TypInt},
		Sym:  idx,
	}}
}

// rewriteIdentSym repoints every Ident bound to from at to, in place. The
// Idents are mutated rather than replaced, so a read-only walk is enough.
func rewriteIdentSym(stmts []ir.Stmt, from ir.Symbol, to *ir.Var) {
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok && id.Sym == from {
			id.Name = to.Name
			id.Sym = to
		}
		return nil
	})
}
