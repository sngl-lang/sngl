package ir

// ReachedThroughIDs is every `#id` something in pkg reads through -- `c.p.opt`,
// `c.p.hits`, `c.p.opt[1].click()` -- which names a component instance a
// test reaches into. Only passNoInlineComponents records what its splice made
// of an instance's members, so a splice anywhere else would leave such a read
// nothing to be pointed at.
func ReachedThroughIDs(pkg *Package) map[string]bool {
	out := map[string]bool{}
	if pkg == nil {
		return out
	}
	_ = Walk(pkg, func(n Node) error {
		var operand Expr
		switch x := n.(type) {
		case *Select:
			operand = x.Operand
		case *Call:
			// A trigger fires the node's own event, reaching into nothing.
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
			out[sel.Field] = true
		}
		return nil
	})
	return out
}
