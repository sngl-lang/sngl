package lower

import (
	"duckfam.us/sngl/ir"
)

// answerTestRecordRead answers a test's read of a field of a record a data
// slot holds -- `c.main.menu[1].children[2].checked` -- with that one record's
// field as its call site wrote it, `sizes[2] == c.size`, a loop's variable
// bound to its list at the index as rewriteTestPropRead binds one: what the
// window drew for the entry. Answering the slot instead and indexing it built
// every record in the test, each handler with it, and a handler writing what
// an instance built at run time reads carries that instance's updates.
//
// Nil where the read is no such field, or the list does not say statically
// which record sits at an index -- an `if` or a forwarded slot before it, or
// a record before a `for` indexed by anything but a literal -- and the slot is
// answered whole as before. An event is a pick, which NameRecordPresses
// answers.
func answerTestRecordRead(sel *ir.Select, tests map[*ir.Param]bool) ir.Expr {
	if t := sel.ExprType(); t == nil || t.Kind == ir.TypeFunc {
		return nil
	}
	first, slots, idx := recordReadPath(sel.Operand)
	if first == nil {
		return nil
	}
	list := rewriteTestPropRead(first, tests)
	if list == nil {
		return nil
	}
	return underAnswer(list, func(list ir.Expr) ir.Expr {
		var rec *ir.StructLit
		for i, ix := range idx {
			if i > 0 {
				if list = pickedField(rec, slots[i]); list == nil {
					return nil
				}
			}
			if rec = recordAt(list, ix); rec == nil {
				return nil
			}
		}
		return pickedField(rec, sel.Field)
	})
}

// recordReadPath reads r as a record reached through data slots: the read of
// the node's slot, the slot read at each level and the index into it.
func recordReadPath(r ir.Expr) (first *ir.Select, slots []string, idx []ir.Expr) {
	for {
		ix, ok := unnarrowed(r).(*ir.Index)
		if !ok {
			break
		}
		sel, ok := unnarrowed(ix.Operand).(*ir.Select)
		if !ok {
			return nil, nil, nil
		}
		slots = append([]string{sel.Field}, slots...)
		idx = append([]ir.Expr{ix.Idx}, idx...)
		first, r = sel, sel.Operand
	}
	if first == nil {
		return nil, nil, nil
	}
	return first, slots, idx
}

// underAnswer is pick applied to what answer, a slot's answer, returns: the
// list itself, or the return of the providers underProviders wrapped it in.
func underAnswer(answer ir.Expr, pick func(ir.Expr) ir.Expr) ir.Expr {
	call, ok := answer.(*ir.Call)
	if !ok {
		return pick(ir.CloneExprSharingDecls(answer))
	}
	l, ok := call.Callee.(*ir.Lambda)
	if !ok || l.Func == nil || len(call.Args) != 0 {
		return pick(ir.CloneExprSharingDecls(answer))
	}
	body := deepCloneStmts(l.Func.Block)
	ret := body
	for len(ret) == 1 {
		p, ok := ret[0].(*ir.ContextProvider)
		if !ok {
			break
		}
		ret = p.Children
	}
	if len(ret) != 1 {
		return nil
	}
	r, ok := ret[0].(*ir.Return)
	if !ok {
		return nil
	}
	if r.Value = pick(r.Value); r.Value == nil {
		return nil
	}
	t := r.Value.ExprType()
	return &ir.Call{Type: t, Callee: &ir.Lambda{Type: ir.FuncOf(nil, t), Func: &ir.Func{Return: t, Block: body}}}
}

// recordAt is the record list holds at ix, where list is what DataFamilies
// wrote and says which one that is.
func recordAt(list ir.Expr, ix ir.Expr) *ir.StructLit {
	lit, ok := list.(*ir.ListLit)
	if !ok {
		return nil
	}
	at, literal := literalInt(ix)
	for i, e := range lit.Elems {
		switch x := e.(type) {
		case *ir.StructLit:
			if !literal {
				return nil
			}
			if at == 0 {
				return x
			}
			at--
		case *ir.Spread:
			iter, fn := mappedRecords(x)
			if fn == nil || !literal && i > 0 {
				return nil
			}
			n, known := knownLength(iter)
			if i < len(lit.Elems)-1 && (!known || !literal) {
				return nil
			}
			if known && literal && at >= n {
				at -= n
				continue
			}
			elem := ix
			if literal {
				elem = intLiteralLit(at)
			}
			return mappedRecord(iter, fn, elem)
		default:
			return nil
		}
	}
	return nil
}

// mappedRecords is the list and the function a `for` among records maps over
// it with, or nil.
func mappedRecords(sp *ir.Spread) (ir.Expr, *ir.Lambda) {
	call, ok := sp.Operand.(*ir.Call)
	if !ok || call.Func == nil || len(call.Args) != 2 {
		return nil, nil
	}
	recv := call.Args[0].Value
	if m, ok := ir.MemberOf(recv.ExprType().Decl, "map"); !ok || m != call.Func {
		return nil, nil
	}
	fn, ok := call.Args[1].Value.(*ir.Lambda)
	if !ok || fn.Func == nil || len(fn.Func.Params) != 1 || len(fn.Func.Block) != 1 {
		return nil, nil
	}
	return recv, fn
}

// mappedRecord is the record fn maps iter's element at ix to.
func mappedRecord(iter ir.Expr, fn *ir.Lambda, ix ir.Expr) *ir.StructLit {
	ret, ok := fn.Func.Block[0].(*ir.Return)
	if !ok {
		return nil
	}
	param := fn.Func.Params[0]
	elemType := param.Type
	elem := &ir.Index{Operand: iter, Idx: ix, Type: elemType}
	rec, ok := newExprWalker(func(x ir.Expr) ir.Expr {
		if id, ok := x.(*ir.Ident); ok && id.Sym == ir.Symbol(param) {
			return ir.CloneExprSharingDecls(elem)
		}
		return x
	}).expr(ir.CloneExprSharingDecls(ret.Value)).(*ir.StructLit)
	if !ok {
		return nil
	}
	return rec
}

// knownLength is how many elements e holds where a literal says so.
func knownLength(e ir.Expr) (int, bool) {
	if id, ok := e.(*ir.Ident); ok {
		if v, ok := id.Sym.(*ir.Var); ok && v.IsConst && v.Init != nil {
			e = v.Init
		}
	}
	lit, ok := e.(*ir.ListLit)
	if !ok {
		return 0, false
	}
	for _, x := range lit.Elems {
		if _, spread := x.(*ir.Spread); spread {
			return 0, false
		}
	}
	return len(lit.Elems), true
}

// pickedField is what rec holds in field, its type's zero where the call site
// wrote nothing and the family declares no default.
func pickedField(rec *ir.StructLit, field string) ir.Expr {
	if rec == nil {
		return nil
	}
	if v := rec.Field(field); v != nil {
		return v
	}
	if rec.Def == nil {
		return nil
	}
	for _, f := range rec.Def.Fields {
		if f.Name == field {
			return ir.ZeroExpr(f.Type)
		}
	}
	return nil
}
