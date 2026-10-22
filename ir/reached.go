package ir

// ReachedHandles is the handle of every node a test reaches into -- `c.p` in
// `c.p.opt`, `c.p.hits` or `c.p.reset()` -- which names a component instance
// whose members the test reads. Only passNoInlineComponents records what its
// splice made of an instance's members, so a splice anywhere else would leave
// such a read nothing to be pointed at. A trigger fires the node's own event
// and reaches into nothing.
func ReachedHandles(pkg *Package) map[*Var]bool {
	out := map[*Var]bool{}
	walkTests(pkg, func(n Node) {
		if sel := reachedSelect(n); sel != nil {
			if h := handleIn(sel.Operand, sel.Field); h != nil {
				out[h] = true
			}
		}
	})
	return out
}

// reachedSelect is the ref n reaches into, or nil.
func reachedSelect(n Node) *Select {
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
	if sel, ok := operand.(*Select); ok && sel.Ref != RefNone {
		return sel
	}
	return nil
}

// NullTestedIDs is every `#id` a test null-tests -- `c.p == null`, `c.p[1] !=
// null`. By id rather than handle, so a node a lowering pass has moved into a
// body of its own -- a block's state, a slot's -- is still found.
func NullTestedIDs(pkg *Package) map[string]bool {
	out := map[string]bool{}
	walkTests(pkg, func(n Node) {
		if e, ok := n.(Expr); ok {
			if sel, _, _, ok := NullTestedRef(e); ok {
				out[sel.Field] = true
			}
		}
	})
	return out
}

// PropReadIDs is every `#id` a test reads a prop of -- `c.label.value`,
// `c.row[1].value`. Such a read is answered from what the node's call site
// wrote, so the call must still be there to read when the lowering asks.
func PropReadIDs(pkg *Package) map[string]bool {
	out := map[string]bool{}
	walkTests(pkg, func(n Node) {
		sel, ok := n.(*Select)
		if !ok {
			return
		}
		ref := sel.Operand
		for {
			switch x := ref.(type) {
			case *Conversion:
				ref = x.Operand
				continue
			case *Index:
				ref = x.Operand
				continue
			}
			break
		}
		if node, ok := ref.(*Select); ok && node.Ref != RefNone {
			out[node.Field] = true
		}
	})
	return out
}

func walkTests(pkg *Package, visit func(Node)) {
	if pkg == nil {
		return
	}
	for _, fn := range pkg.Funcs {
		if fn != nil && fn.IsTest {
			_ = Walk(fn.Block, func(n Node) error {
				visit(n)
				return nil
			})
		}
	}
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
