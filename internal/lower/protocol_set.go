package lower

import (
	"fmt"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// spliceSets makes each write of a prop whose component declares its
// #[gen.protocol(set)] the call that set makes: the write's value becomes the
// set's body with the node and the value bound, so a string it hands the host
// is a temp ReleaseTemps binds and frees around the write. Every write, not
// only the view's first: an updater and a handler write a node's prop too,
// and both were made before this pass. A write one block shares with
// another is spliced once.
func spliceSets(pkg *ir.Package, caps Features) error {
	var err error
	done := map[*ir.Assign]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		a, ok := n.(*ir.Assign)
		if !ok || a.Op != ast.AssignSet || done[a] {
			return nil
		}
		done[a] = true
		sel, ok := a.Target.(*ir.Select)
		if !ok {
			return nil
		}
		node, ok := sel.Operand.(*ir.Ident)
		if !ok || !node.IsElementRef || node.Type == nil {
			return nil
		}
		comp, _ := node.Type.Decl.(*ir.Component)
		if comp == nil {
			return nil
		}
		fn := comp.Protocol.Setter(sel.Field)
		if fn == nil {
			return nil
		}
		value, e := setCall(fn, node, a.Value, caps)
		if e != nil {
			err = fmt.Errorf("%s.%s: %w", comp.Name, sel.Field, e)
			return ir.SkipAll
		}
		a.Value = value
		return nil
	})
	return err
}

func setCall(fn *ir.Func, node ir.Expr, value ir.Expr, caps Features) (ir.Expr, error) {
	call, err := protocolSplice(fn, []ir.Expr{node, value})
	if err != nil {
		return nil, err
	}
	if call, err = foldSplice(call, fn); err != nil {
		return nil, err
	}
	// passTernary ran before the op existed, and there is no statement list
	// to hoist into here: a value the set computes from a bool is a call in
	// place.
	if !caps.Ternary {
		call = (&ternState{}).immediateTernaries(call)
	}
	return call, nil
}

// foldSplice takes, in a spliced body, the arm of each match and the branch
// of each ternary a literal head selects. A match in a set maps a word the
// program writes to the host's value for it, and passMatch ran before the op
// existed, so its subject must be a literal one of its arms names: its `else`
// is there because a match over a string must have one, and is never taken.
func foldSplice(e ir.Expr, fn *ir.Func) (ir.Expr, error) {
	var err error
	fold := func(n ir.Node) (ir.Node, error) {
		switch x := n.(type) {
		case *ir.MatchExpr:
			lit, ok := x.Subject.(*ir.Literal)
			if !ok {
				err = fmt.Errorf("%s folds its match where the prop is written, and only a literal value folds", fn.Name)
				return n, ir.SkipAll
			}
			for _, arm := range x.Arms {
				for _, p := range arm.Patterns {
					if pl, ok := p.(*ir.Literal); ok && pl.Value == lit.Value {
						return arm.Value, nil
					}
				}
			}
			err = fmt.Errorf("%q is none of the values %s names", lit.Value, fn.Name)
			return n, ir.SkipAll
		case *ir.Ternary:
			if b, ok := literalBool(x.Cond); ok {
				if b {
					return x.Then, nil
				}
				return x.Else, nil
			}
		}
		return n, nil
	}
	holder := &ir.Return{Value: e}
	_ = ir.Rewrite(holder, fold)
	return holder.Value, err
}

// literalBool is the value of a bool literal, or of `true` or `false`, which
// are declared as the comparison of two literals.
func literalBool(e ir.Expr) (bool, bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type != nil && x.Type.Kind == ir.TypeBool {
			b, err := strconv.ParseBool(x.Value)
			return b, err == nil
		}
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && v.IsConst && v.Init != nil {
			return literalBool(v.Init)
		}
	case *ir.Binary:
		l, lok := x.Left.(*ir.Literal)
		r, rok := x.Right.(*ir.Literal)
		if lok && rok && x.Op == ast.BinEq {
			return l.Value == r.Value, true
		}
	}
	return false, false
}
