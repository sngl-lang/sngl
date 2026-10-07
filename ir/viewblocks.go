package ir

// The view statements that hold other view statements, and which of their
// blocks are view: the one list every walk over a view tree reads, rather than
// a switch of its own in each. Hand-written copies of that switch drifted one
// member at a time -- each missed a different one of a boundary's fallback, a
// provider, a for's else or a slot insertion's fallback -- and each gap was a
// silent acceptance or a construct no backend saw.
//
// ViewBlocks reaches no handler, lambda or function body: those are
// imperative, and a walk wanting them wants ir.Walk.

// ViewBlocks is every block of view statements s holds: TransparentBlocks', a node's
// children and each population of its slots, and a slot insertion's fallback
// and its populations of the slot's entries. Slots come in name order.
func ViewBlocks(s Stmt) []*[]Stmt {
	switch n := s.(type) {
	case *NodeInst:
		out := []*[]Stmt{&n.Children}
		for _, name := range SlotNames(n.Slots) {
			if sc := n.Slots[name]; sc != nil {
				out = append(out, &sc.Body)
			}
		}
		return out
	case *SlotInst:
		out := []*[]Stmt{&n.Children}
		for _, name := range SlotNames(n.Slots) {
			if sc := n.Slots[name]; sc != nil {
				out = append(out, &sc.Body)
			}
		}
		return out
	}
	return TransparentBlocks(s)
}

// WalkView calls visit on each statement of a view tree, depth first and in
// order, and descends through ViewBlocks into what it holds unless visit
// returns false.
func WalkView(stmts []Stmt, visit func(Stmt) bool) {
	for _, s := range stmts {
		if !visit(s) {
			continue
		}
		for _, b := range ViewBlocks(s) {
			WalkView(*b, visit)
		}
	}
}
