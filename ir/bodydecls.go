package ir

// BodyOwners maps each component a body declared to the component that
// declared it. Nil when the package has no body-local component.
//
// Read from BodyDecls rather than from a scope, so it answers after the
// checker as well as during it.
func BodyOwners(pkg *Package) map[*Component]*Component {
	if pkg == nil {
		return nil
	}
	var owners map[*Component]*Component
	for _, owner := range pkg.Components {
		for _, sym := range owner.BodyDecls {
			nested, ok := sym.(*Component)
			if !ok {
				continue
			}
			if owners == nil {
				owners = map[*Component]*Component{}
			}
			owners[nested] = owner
		}
	}
	return owners
}

// CapturesEnclosingState reports whether nested names a var, prop or func of a
// body it was written inside. False for a component no body declared.
//
// Both the checker and the inliner ask this, of the same graph: capture is
// lowered as the owner's own state, so the two have to agree on which
// declarations carry one.
func CapturesEnclosingState(nested *Component, owners map[*Component]*Component) bool {
	enclosing := map[Symbol]bool{}
	for owner := owners[nested]; owner != nil; owner = owners[owner] {
		for _, v := range owner.Vars {
			enclosing[v] = true
		}
		for _, p := range owner.Props {
			// Sym is minted on the first reference in a checked body, so a
			// prop nothing names has none and cannot be the one captured here.
			if p.Sym != nil {
				enclosing[p.Sym] = true
			}
		}
		// Every func, receiver or none: a nested body reaches an owner's
		// method by bare name too, and that call needs the same splice.
		for _, f := range owner.Funcs {
			enclosing[f] = true
		}
	}
	if len(enclosing) == 0 {
		return false
	}
	found := false
	_ = Walk(nested, func(n Node) error {
		switch e := n.(type) {
		case *Ident:
			if e.Sym != nil && enclosing[e.Sym] {
				found = true
			}
		case *Call:
			// A call names its callee on Func rather than through an Ident.
			if e.Func != nil && enclosing[e.Func] {
				found = true
			}
		}
		return nil
	})
	return found
}
