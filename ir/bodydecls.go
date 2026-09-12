package ir

// eachBody calls yield with the state of comp's live body and of every
// override it carries. The overrides are needed because the checker asks these
// questions with no target picked and the live slots then hold the base
// declaration's body; after specialization the active override is the live one
// too, and both callers build sets, so seeing it twice costs nothing.
func eachBody(comp *Component, yield func(b Body)) {
	yield(Body{Vars: comp.Vars, BodyDecls: comp.BodyDecls, Funcs: comp.Funcs})
	for _, b := range comp.PlatformOverrides {
		yield(b)
	}
	for _, b := range comp.LanguageOverrides {
		yield(b)
	}
}

// BodyFuncs is every func any of comp's bodies declares -- its own and each
// override's. The checker needs it because a func written in an override body
// is owned by that component and has to be checked in its scope, while
// Component.Funcs holds only the live body's by the time pass2 asks.
func BodyFuncs(comp *Component) []*Func {
	var out []*Func
	seen := map[*Func]bool{}
	eachBody(comp, func(b Body) {
		for _, fn := range b.Funcs {
			if !seen[fn] {
				seen[fn] = true
				out = append(out, fn)
			}
		}
	})
	return out
}

// BodyOwners maps each component a body declared to the component that
// declared it, read from BodyDecls so it answers after the checker too. Nil
// when the package has no body-local component.
func BodyOwners(pkg *Package) map[*Component]*Component {
	if pkg == nil {
		return nil
	}
	var owners map[*Component]*Component
	for _, owner := range pkg.Components {
		eachBody(owner, func(b Body) {
			for _, sym := range b.BodyDecls {
				nested, ok := sym.(*Component)
				if !ok {
					continue
				}
				if owners == nil {
					owners = map[*Component]*Component{}
				}
				owners[nested] = owner
			}
		})
	}
	return owners
}

// CapturesEnclosingState reports whether nested names a var, prop or func of a
// body it was written inside. False for a component no body declared.
func CapturesEnclosingState(nested *Component, owners map[*Component]*Component) bool {
	enclosing := map[Symbol]bool{}
	for owner := owners[nested]; owner != nil; owner = owners[owner] {
		eachBody(owner, func(b Body) {
			for _, v := range b.Vars {
				enclosing[v] = true
			}
			// A method is reached by receiver rather than by name, and a
			// nested body reaches its owner's through lookupBodyMethod -- so
			// an override's helpers are captured exactly as the base's are.
			for _, fn := range b.Funcs {
				enclosing[fn] = true
			}
		})
		for _, p := range owner.Props {
			// Sym is minted on the first reference, so a prop nothing names
			// has none.
			if p.Sym != nil {
				enclosing[p.Sym] = true
			}
		}
		// Receiver or none: a method is reached by bare name here too.
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
			// A call names its callee on Func, not through an Ident.
			if e.Func != nil && enclosing[e.Func] {
				found = true
			}
		}
		return nil
	})
	return found
}
