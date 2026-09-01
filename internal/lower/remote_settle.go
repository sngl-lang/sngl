package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// RemoteSettleFunc is the name of the handler a store calls when one of its
// boxes answers. A platform that patches its widgets rather than re-rendering
// has to be told; one that re-renders on any event only needs waking.
const RemoteSettleFunc = "__remoteSettled"

// synthesizeRemoteSettle builds that handler for one owner: the updaters of
// every prop and slot that reads a query box, with nothing in front of them.
//
// Read-triggered fetching means the render that started a fetch is over before
// there is anything to show, so the box changes with no assignment anywhere for
// the ordinary updater injection to hang on. This is that injection, fired by
// the answer instead.
//
// Both spellings need it and for the same reason. A snapshot box is a var, so
// its props are keyed on that var; a followed box is a lookup inlined into the
// prop, keyed on the cells it follows. Neither is written when the fetch
// answers -- what changed is inside the box.
func (st *reactivityState) synthesizeRemoteSettle() {
	var props []reactiveProp
	var slots []reactiveSlot
	seen := map[*ir.Func]bool{}
	for v, deps := range st.reverseDeps {
		for _, p := range deps {
			if readsRemote(p.Expr) || isRemoteVar(v) {
				props = append(props, p)
			}
		}
	}
	for v, dep := range st.reverseSlots {
		if !isRemoteVar(v) {
			continue
		}
		for _, sl := range dep {
			if sl.GenFunc == nil || seen[sl.GenFunc] {
				continue
			}
			seen[sl.GenFunc] = true
			slots = append(slots, sl)
		}
	}
	if len(props) == 0 && len(slots) == 0 {
		return
	}
	// Deterministic output: the map walks above are unordered, and these
	// statements are emitted in the order they are built.
	sortProps(props)

	body := st.updaterStmts(props, slots, nil)
	if len(body) == 0 {
		return
	}
	st.owner.addFunc(&ir.Func{
		Name:   RemoteSettleFunc,
		Return: ir.TypVoid,
		Purity: ir.PurityMutates,
		Block:  body,
	})
}

// sortProps orders by node then key, which is the order the build path
// declared them in and the order a reader compares two builds by.
func sortProps(props []reactiveProp) {
	for i := 1; i < len(props); i++ {
		for j := i; j > 0 && less(props[j], props[j-1]); j-- {
			props[j], props[j-1] = props[j-1], props[j]
		}
	}
}

func less(a, b reactiveProp) bool {
	if a.NodeID != b.NodeID {
		return a.NodeID < b.NodeID
	}
	return a.Key < b.Key
}

// isRemoteVar reports whether a var holds a query box.
func isRemoteVar(v *ir.Var) bool {
	return v != nil && v.Type != nil && v.Type.Kind == ir.TypeRemote
}

// readsRemote reports whether an expression looks a box up, which a followed
// query's prop does directly -- the lookup was inlined into it.
func readsRemote(e ir.Expr) bool {
	found := false
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if c, isCall := x.(*ir.Call); isCall && c.Func != nil && c.Func.Intrinsic == remoteQueryIntrinsic {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
