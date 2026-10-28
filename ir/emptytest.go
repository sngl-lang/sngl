package ir

import (
	"strconv"

	"duckfam.us/sngl/ast"
)

// EmptyTest returns a bool expression that holds exactly when n's loop body
// would not run — the iterable yields nothing — or nil when the loop's head is
// not one this can measure.
//
// Re-evaluated, not stored: a caller must check Reevaluable first.
//
// A counted sequence is measured from its bounds, having no elements until a
// host loop counts them; its step is a constant, so the direction of the
// comparison is settled here.
//
// clone must share symbols with the original (lower.deepCloneExpr, not
// ir.CloneExpr, which deep-copies an Ident.Sym and so reads a binding nothing
// else mentions). Nil asks only whether a test exists: the result then aliases
// n's head and must not be spliced into the tree.
func EmptyTest(n *For, clone func(Expr) Expr) Expr {
	if n == nil || n.Iter == nil {
		return nil
	}
	if clone == nil {
		clone = func(e Expr) Expr { return e }
	}
	if c := CountedSeq(n); c != nil {
		op := ast.BinGte
		if c.Step < 0 {
			op = ast.BinLte
		}
		return &Binary{Type: TypBool, Op: op, Left: clone(c.Start), Right: clone(c.End)}
	}
	length := iterLength(n, clone)
	if length == nil {
		return nil
	}
	return &Binary{
		Type:  TypBool,
		Op:    ast.BinEq,
		Left:  length,
		Right: &Literal{Type: TypInt, Value: "0"},
	}
}

// iterLength is the length of the list or map n walks, or nil for any other
// iterable.
func iterLength(n *For, clone func(Expr) Expr) *Call {
	t := n.Iter.ExprType()
	if t == nil {
		return nil
	}
	var intrinsic string
	switch t.Kind {
	case TypeList:
		intrinsic = "list.length"
	case TypeMap:
		intrinsic = "map.length"
	default:
		// Asking a pull sequence consumes the element that answers.
		return nil
	}
	var params []*Param
	if def := LookupIntrinsic(intrinsic); def != nil {
		params, _ = def.Instantiate(t.Elems...)
	}
	return &Call{
		Type: TypInt,
		Func: &Func{
			Name:      "length",
			Receiver:  t,
			Intrinsic: intrinsic,
			Return:    TypInt,
			Params:    params,
			Purity:    PurityPure,
		},
		Args: []CallArg{{Value: clone(n.Iter)}},
	}
}

// CopyCount is how many copies the loop n renders: the length of the list or
// map it walks, or how many numbers the counted sequence counts. Nil for any
// other iterable.
func CopyCount(n *For, clone func(Expr) Expr) Expr {
	if n == nil || n.Iter == nil {
		return nil
	}
	if clone == nil {
		clone = func(e Expr) Expr { return e }
	}
	c := CountedSeq(n)
	if c == nil {
		if length := iterLength(n, clone); length != nil {
			return length
		}
		return nil
	}
	// (end - start + step - 1) / step numbers short of the end, none where the
	// start is already past it; the far side for a step down.
	from, to, step, past := clone(c.Start), clone(c.End), c.Step, ast.BinGte
	if step < 0 {
		from, to, step, past = to, from, -step, ast.BinLte
	}
	lit := func(n int) Expr { return &Literal{Type: TypInt, Value: strconv.Itoa(n)} }
	span := &Binary{Type: TypInt, Op: ast.BinSub, Left: to, Right: from}
	if step > 1 {
		span = &Binary{Type: TypInt, Op: ast.BinDiv, Left: &Binary{Type: TypInt, Op: ast.BinAdd, Left: span, Right: lit(step - 1)}, Right: lit(step)}
	}
	return &Ternary{
		Type: TypInt,
		Cond: &Binary{Type: TypBool, Op: past, Left: clone(c.Start), Right: clone(c.End)},
		Then: lit(0),
		Else: span,
	}
}

// SeqIntrinsic is the sngl:seq constructor n's head calls, or "" for any other
// head. CountedSeq says whether a sequence is measurable; this says whether it
// is a sequence at all, which is what separates "wrong iterable" from "right
// iterable, unreadable bounds" in a diagnostic.
func SeqIntrinsic(n *For) string {
	if n == nil {
		return ""
	}
	call, ok := n.Iter.(*Call)
	if !ok || call.Func == nil {
		return ""
	}
	switch call.Func.Intrinsic {
	case "seq.count", "seq.range", "seq.step":
		return call.Func.Intrinsic
	}
	return ""
}

// Reevaluable reports whether e can be evaluated a second time without
// changing what the program does.
func Reevaluable(e Expr) bool {
	ok := true
	_ = Walk(e, func(n Node) error {
		c, isCall := n.(*Call)
		if !isCall {
			return nil
		}
		switch {
		case c.Func == nil:
			ok = false
		case c.Func.Purity != PurityPure && c.Func.Purity != PurityReadonly:
			ok = false
		default:
			if def := LookupIntrinsic(c.Func.Intrinsic); def != nil && def.MutatesReceiver {
				ok = false
			}
		}
		return nil
	})
	return ok
}

// CopyInRange is whether the loop n renders a copy at index idx: the index
// below the list's length, or the counted sequence's number there short of its
// end. Nil for any other iterable.
func CopyInRange(n *For, idx Expr, clone func(Expr) Expr) Expr {
	if n == nil || n.Iter == nil {
		return nil
	}
	if clone == nil {
		clone = func(e Expr) Expr { return e }
	}
	if c := CountedSeq(n); c != nil {
		at := &Binary{Type: TypInt, Op: ast.BinAdd, Left: clone(c.Start), Right: &Binary{Type: TypInt, Op: ast.BinMul, Left: idx, Right: &Literal{Type: TypInt, Value: strconv.Itoa(c.Step)}}}
		op := ast.BinLt
		if c.Step < 0 {
			op = ast.BinGt
		}
		return &Binary{Type: TypBool, Op: op, Left: at, Right: clone(c.End)}
	}
	t := n.Iter.ExprType()
	if t == nil || t.Kind != TypeList {
		return nil
	}
	empty, _ := EmptyTest(n, clone).(*Binary)
	if empty == nil {
		return nil
	}
	return &Binary{Type: TypBool, Op: ast.BinLt, Left: idx, Right: empty.Left}
}
