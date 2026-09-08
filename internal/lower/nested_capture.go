package lower

import "git.duckfam.us/jonathan/sngl/ir"

// spliceNestedCaptures substitutes every instantiation of a capturing
// body-local component into the body that declared it, before that body is
// inlined anywhere else.
//
// A captured var is the owner's own state (#202), so the reference has to be
// renamed by the owner's per-instance rename -- and expandCall renames the body
// it splices, not the separate declaration a NodeInst inside it points at. Left
// to the main walk, a nested body reached it after its owner was already
// spliced and gone, and emitted the captured name with nothing declaring it.
//
// Only the capturing ones: a nested component that reads nothing of its owner
// is an ordinary component whose name happens to be private, and the main walk
// places it at the call site as before.
func (st *inlineCompState) spliceNestedCaptures() error {
	owners := ir.BodyOwners(st.pkg)
	if len(owners) == 0 {
		return nil
	}
	capturing := map[*ir.Component]bool{}
	for nested := range owners {
		if st.inlinable(nested) && ir.CapturesEnclosingState(nested, owners) {
			capturing[nested] = true
		}
	}
	if len(capturing) == 0 {
		return nil
	}
	st.captureOnly = capturing
	defer func() { st.captureOnly = nil }()
	// Package order, which puts a nested declaration ahead of its owner: an
	// innermost body is spliced into the one that declared it first, so what
	// the next round sees is already free of capture.
	for _, owner := range st.pkg.Components {
		if !declaresAny(owner, capturing) {
			continue
		}
		st.hoist = componentHoist(owner)
		for {
			body, changed, err := st.inlineStmts(owner.Body)
			if err != nil {
				return err
			}
			owner.Body = body
			if !changed {
				break
			}
		}
	}
	return nil
}

// declaresAny reports whether owner's body declared any component in set.
func declaresAny(owner *ir.Component, set map[*ir.Component]bool) bool {
	for _, sym := range owner.BodyDecls {
		if nested, ok := sym.(*ir.Component); ok && set[nested] {
			return true
		}
	}
	return false
}
