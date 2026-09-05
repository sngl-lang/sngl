package ir

import "git.duckfam.us/jonathan/sngl/ast"

// EmptyTest returns a bool expression that holds exactly when n's loop body
// would not run — the iterable yields nothing — or nil when the loop's head is
// not one this can measure.
//
// It is the view-body counterpart of the flag passForElse sets in an
// imperative body. There the general statement of "the body never ran" is a
// variable, because a condition loop has no iterable at all; here the head is
// always an iterable (the checker refuses the other two forms in a view body)
// and a variable is what a static renderer cannot hold, so the question is
// asked of the iterable instead and asked again on every re-render.
//
// The answer is therefore re-evaluated rather than stored, which is only sound
// for an iterable that can be evaluated twice: Reevaluable is that condition,
// and the checker refuses a view for-else whose head fails it rather than
// letting this double-evaluate a side effect.
//
// A counted sequence is measured from its bounds instead of its elements,
// since it has none until a host loop counts them. The step is a constant, so
// which comparison says "empty" is decided here rather than at run time.
//
// clone is the caller's structural copier, applied to each piece of the loop
// head that ends up in a second position. It is a parameter because the one
// that is correct here shares symbols with the original -- an Ident.Sym is the
// binding a later pass matches on, and a copy that duplicated it would be a
// read of a variable nothing else mentions. ir.CloneExpr is the other kind and
// is wrong for this. Nil asks only whether a test exists at all: the result
// then aliases the loop's own head and must not be spliced into the tree.
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
		// An iter<T> that is not a counted sequence is a pull sequence, and
		// asking one whether it is empty consumes the element that answers.
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

// Reevaluable reports whether e can be evaluated a second time without
// changing what the program does. A read of mutable state qualifies -- that is
// what every ordinary iterable is -- but a call that mutates, or one whose
// effects nothing has established, does not.
//
// The question is asked of an expression a caller intends to duplicate. It is
// deliberately about side effects and not about cost: re-reading a list twice
// is free, and re-running a pure filter is not, but only the first is a
// correctness question.
func Reevaluable(e Expr) bool {
	ok := true
	_ = Walk(e, func(n Node) error {
		c, isCall := n.(*Call)
		if !isCall {
			return nil
		}
		switch {
		case c.Func == nil:
			// A call through a value: nothing declares what it costs.
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
