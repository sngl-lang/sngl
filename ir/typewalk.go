package ir

import "slices"

// Any reports whether pred holds of t or of any type inside it: its elements,
// its signature's parameters and result, and an anonymous struct's fields.
// No cycle guard: a field cannot name a type that has no name.
func (t *Type) Any(pred func(*Type) bool) bool {
	if t == nil {
		return false
	}
	if pred(t) {
		return true
	}
	if slices.ContainsFunc(t.Elems, func(e *Type) bool { return e.Any(pred) }) {
		return true
	}
	if t.Sig != nil {
		if slices.ContainsFunc(t.Sig.Params, func(p *Param) bool { return p.Type.Any(pred) }) {
			return true
		}
		if t.Sig.Return.Any(pred) {
			return true
		}
	}
	if sd := AnonStructDef(t); sd != nil {
		return slices.ContainsFunc(sd.Fields, func(f *StructField) bool { return f.Type.Any(pred) })
	}
	return false
}

// MapChildren is t with f applied to each type directly inside it -- its
// elements, its signature's parameters and result, and an un-interned
// anonymous struct's fields, which are part of its type and reachable
// nowhere else -- and t itself where f changes none. Every rewrite of a type
// is f recursing through this: Substitute, and the lowering's rewrites.
//
// An interned anonymous struct's fields are left to its declaration, which a
// package walk reaches; rebuilt here, it would become a copy nobody interned.
func (t *Type) MapChildren(f func(*Type) *Type) *Type {
	if t == nil {
		return nil
	}
	if sd := AnonStructDef(t); sd != nil && IsUninternedAnon(sd) {
		var fields []*StructField
		for i, fd := range sd.Fields {
			ft := f(fd.Type)
			if ft == fd.Type && fields == nil {
				continue
			}
			if fields == nil {
				fields = slices.Clone(sd.Fields[:i])
			}
			cp := *fd
			cp.Type = ft
			fields = append(fields, &cp)
		}
		if fields == nil {
			return t
		}
		return (&StructDef{Pkg: sd.Pkg, Fields: fields, Anon: true}).SymType()
	}
	var elems []*Type
	for i, e := range t.Elems {
		ne := f(e)
		if ne == e && elems == nil {
			continue
		}
		if elems == nil {
			elems = slices.Clone(t.Elems[:i])
		}
		elems = append(elems, ne)
	}
	sig := t.Sig.mapTypes(f)
	if elems == nil && sig == t.Sig {
		return t
	}
	cp := *t
	if elems != nil {
		cp.Elems = elems
	}
	cp.Sig = sig
	return &cp
}

// mapTypes is s with f applied to each parameter's type and the result's, s
// itself where f changes none.
func (s *FuncSig) mapTypes(f func(*Type) *Type) *FuncSig {
	if s == nil {
		return nil
	}
	var params []*Param
	for i, p := range s.Params {
		nt := f(p.Type)
		if nt == p.Type && params == nil {
			continue
		}
		if params == nil {
			params = slices.Clone(s.Params[:i])
		}
		cp := *p
		cp.Type = nt
		params = append(params, &cp)
	}
	ret := f(s.Return)
	if params == nil && ret == s.Return {
		return s
	}
	cp := *s
	if params != nil {
		cp.Params = params
	}
	cp.Return = ret
	return &cp
}
