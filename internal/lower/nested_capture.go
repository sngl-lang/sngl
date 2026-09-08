package lower

import "git.duckfam.us/jonathan/sngl/ir"

// spliceNestedCaptures substitutes every instantiation of a capturing
// body-local component into the body that declared it, before that body is
// inlined anywhere else — expandCall renames the body it splices and not the
// declaration a NodeInst in it points at, so the capture has to be inside the
// owner by then (#202). Non-capturing ones are left to the main walk.
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
	// Package order puts a nested declaration ahead of its owner, so an
	// innermost body is spliced first.
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
