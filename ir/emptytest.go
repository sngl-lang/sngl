package ir

import "git.duckfam.us/jonathan/sngl/ast"

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
	t := n.Iter.ExprType()
	if t == nil {
		return nil
	}
	var intrinsic, recv string
	switch t.Kind {
	case TypeList:
		intrinsic, recv = "list.length", "list"
	case TypeMap:
		intrinsic, recv = "map.length", "map"
	default:
		// Asking a pull sequence consumes the element that answers.
		return nil
	}
	var params []*Param
	if def := LookupIntrinsic(intrinsic); def != nil {
		params, _ = def.Instantiate(t.Elems...)
	}
	length := &Call{
		Type: TypInt,
		Func: &Func{
			Name:      "length",
			Receiver:  recv,
			Intrinsic: intrinsic,
			Return:    TypInt,
			Params:    params,
			Purity:    PurityPure,
		},
		Args: []CallArg{{Value: clone(n.Iter)}},
	}
	return &Binary{
		Type:  TypBool,
		Op:    ast.BinEq,
		Left:  length,
		Right: &Literal{Type: TypInt, Value: "0"},
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
