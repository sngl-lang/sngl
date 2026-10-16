package ir

// ReachedHandles is the handle of every node a test reaches into -- `c.p` in
// `c.p.opt`, `c.p.hits` or `c.p.reset()` -- which names a component instance
// whose members the test reads. Only passNoInlineComponents records what its
// splice made of an instance's members, so a splice anywhere else would leave
// such a read nothing to be pointed at. A trigger fires the node's own event
// and reaches into nothing.
func ReachedHandles(pkg *Package) map[*Var]bool {
	out := map[*Var]bool{}
	if pkg == nil {
		return out
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = Walk(fn.Block, func(n Node) error {
			var operand Expr
			switch x := n.(type) {
			case *Select:
				operand = x.Operand
			case *Call:
				if x.Event == "" {
					operand = x.Receiver
				}
			}
			if ix, ok := operand.(*Index); ok {
				operand = ix.Operand
			}
			if conv, ok := operand.(*Conversion); ok {
				operand = conv.Operand
			}
			sel, ok := operand.(*Select)
			if !ok || sel.Ref == RefNone {
				return nil
			}
			if h := handleIn(sel.Operand, sel.Field); h != nil {
				out[h] = true
			}
			return nil
		})
	}
	return out
}

// handleIn is the handle of the node with the id in the body of the component
// x is an instance of.
func handleIn(x Expr, id string) *Var {
	if conv, ok := x.(*Conversion); ok {
		x = conv.Operand
	}
	if ix, ok := x.(*Index); ok {
		x = ix.Operand
	}
	t := x.ExprType()
	for t != nil && (t.Kind == TypeOption || t.Kind == TypeList) && len(t.Elems) == 1 {
		t = t.Elems[0]
	}
	if t == nil || t.Kind != TypeComponent {
		return nil
	}
	comp, ok := t.Decl.(*Component)
	if !ok {
		return nil
	}
	var h *Var
	WalkView(comp.Stmts, func(s Stmt) bool {
		if n, ok := s.(*NodeInst); ok && n.ID == id && n.Handle != nil {
			h = n.Handle
			return false
		}
		return h == nil
	})
	return h
}
