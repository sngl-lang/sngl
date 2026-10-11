package ir

import "reflect"

// InstantiateFunc copies fn with its type parameters bound: every type in the
// copy naming one of them names its binding instead. What fn declares -- its
// parameters, its locals and loop variables, and the lambdas in its body with
// theirs -- is copied, and everything it refers to beyond that is shared, so a
// call in the copy still resolves to the declaration the original's did.
func InstantiateFunc(fn *Func, bindings map[string]*Type) *Func {
	if fn == nil {
		return nil
	}
	c := newCloner()
	c.shareDecls = true
	c.subst = bindings
	c.own = ownDecls(fn)
	out := c.clone(reflect.ValueOf(fn)).Interface().(*Func)
	out.TypeParams = nil
	out.RecvTypeParams = nil
	return out
}

// ownDecls is the set of declarations fn's own text declares.
func ownDecls(fn *Func) map[uintptr]bool {
	own := map[uintptr]bool{}
	addFunc := func(f *Func) {
		own[reflect.ValueOf(f).Pointer()] = true
		for _, p := range f.Params {
			own[reflect.ValueOf(p).Pointer()] = true
		}
		if f.RecvParam != nil {
			own[reflect.ValueOf(f.RecvParam).Pointer()] = true
		}
	}
	addFunc(fn)
	_ = Walk(fn.Block, func(n Node) error {
		switch x := n.(type) {
		case *LocalVar:
			if x.Sym != nil {
				own[reflect.ValueOf(x.Sym).Pointer()] = true
			}
		case *For:
			for _, v := range []*LoopVar{x.KeySym, x.ValueSym} {
				if v != nil {
					own[reflect.ValueOf(v).Pointer()] = true
				}
			}
		case *Lambda:
			if x.Func != nil {
				addFunc(x.Func)
			}
		case *Closure:
			if x.Func != nil {
				addFunc(x.Func)
			}
		}
		return nil
	})
	return own
}

// UnifyTypeParams binds the type parameters pattern names from the type
// concrete has in the same position, leaving a parameter already bound as it
// is.
func UnifyTypeParams(pattern, concrete *Type, bindings map[string]*Type) {
	if pattern == nil || concrete == nil {
		return
	}
	if pattern.Kind == TypeTypeParam {
		if _, ok := bindings[pattern.ParamName]; !ok && !concrete.HasTypeParam() {
			bindings[pattern.ParamName] = concrete
		}
		return
	}
	if pattern.Kind != concrete.Kind {
		return
	}
	if len(pattern.Elems) == len(concrete.Elems) {
		for i := range pattern.Elems {
			UnifyTypeParams(pattern.Elems[i], concrete.Elems[i], bindings)
		}
	}
	if pattern.Sig != nil && concrete.Sig != nil && len(pattern.Sig.Params) == len(concrete.Sig.Params) {
		for i := range pattern.Sig.Params {
			UnifyTypeParams(pattern.Sig.Params[i].Type, concrete.Sig.Params[i].Type, bindings)
		}
		UnifyTypeParams(pattern.Sig.Return, concrete.Sig.Return, bindings)
	}
}

// HasTypeParam reports whether t names a type parameter anywhere in it.
func (t *Type) HasTypeParam() bool {
	if t == nil {
		return false
	}
	if t.Kind == TypeTypeParam {
		return true
	}
	for _, e := range t.Elems {
		if e.HasTypeParam() {
			return true
		}
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			if p.Type.HasTypeParam() {
				return true
			}
		}
		return t.Sig.Return.HasTypeParam()
	}
	return false
}
