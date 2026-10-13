package lower

import (
	"slices"

	"duckfam.us/sngl/ir"
)

// passAnonStructs points every anonymous struct a substitution made at the
// package's own declaration with those fields. `struct { v T }` in a generic
// signature is a declaration nobody interned -- its fields mean the
// parameters in scope where it was written -- and ir.Type.Substitute builds
// another of those at each binding, so `struct { v int }` at a call, or in a
// copy passMonomorphize made, names a declaration no package declares. A
// backend would spell it and declare nothing. The interned `struct { v int }`
// the literal `{v=1}` is typed as is the same type (Type.Equal compares
// anonymous structs by fields), so a value crosses between the two with no
// conversion; where the package has none, one is declared under the name the
// checker would have given it.
//
// It runs on every target, after passMonomorphize: a target keeping a generic
// function whole still sees a call's result at the bound type. An anonymous
// struct still naming a type parameter is left as it is, as a generic
// struct's instantiation is.
var passAnonStructs = pass{
	name:    "AnonStructs",
	enabled: func(Features) bool { return true },
	apply:   lowerAnonStructs,
}

type anonStructs struct {
	pkg      *ir.Package
	names    map[string]bool
	interned []*ir.StructDef
}

func lowerAnonStructs(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	a := &anonStructs{pkg: pkg, names: map[string]bool{}}
	for _, sd := range pkg.Structs {
		a.names[sd.Name] = true
		if sd.Anon {
			a.interned = append(a.interned, sd)
		}
	}
	// A literal the optimizer inlined out of a generic body keeps the type it
	// was checked at there, `struct { v T }`, though its values are concrete
	// now: an anonymous struct's type is its fields', so it is retyped from
	// them, innermost first.
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if lit, ok := n.(*ir.StructLit); ok {
			retypeLiteral(lit)
		}
		return nil
	})
	ir.RewriteTypes(pkg, a.rewrite)
	// A literal names its declaration as well as its type.
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if lit, ok := n.(*ir.StructLit); ok && lit.Def != nil && ir.IsUninternedAnon(lit.Def) && lit.Type != nil {
			if sd, ok := lit.Type.Decl.(*ir.StructDef); ok {
				lit.Def = sd
			}
		}
		return nil
	})
	return nil
}

// rewrite is t with every concrete un-interned anonymous struct in it
// replaced by the package's declaration of it, t where there is none.
func (a *anonStructs) rewrite(t *ir.Type) *ir.Type {
	if t == nil {
		return t
	}
	if sd := ir.AnonStructDef(t); sd != nil && ir.IsUninternedAnon(sd) {
		if t.HasTypeParam() {
			return t
		}
		fields := make([]*ir.StructField, len(sd.Fields))
		for i, f := range sd.Fields {
			fields[i] = &ir.StructField{Name: f.Name, Type: a.rewrite(f.Type)}
		}
		return a.declared(fields).SymType()
	}
	elems := t.Elems
	changed := false
	for i, e := range t.Elems {
		if ne := a.rewrite(e); ne != e {
			if !changed {
				elems = slices.Clone(t.Elems)
				changed = true
			}
			elems[i] = ne
		}
	}
	var sig *ir.FuncSig
	if t.Kind == ir.TypeFunc && t.Sig != nil {
		params := make([]*ir.Param, len(t.Sig.Params))
		for i, p := range t.Sig.Params {
			params[i] = p
			if nt := a.rewrite(p.Type); nt != p.Type {
				cp := *p
				cp.Type = nt
				params[i] = &cp
				changed = true
			}
		}
		ret := a.rewrite(t.Sig.Return)
		if ret != t.Sig.Return {
			changed = true
		}
		s := *t.Sig
		s.Params, s.Return = params, ret
		sig = &s
	}
	if !changed {
		return t
	}
	cp := *t
	cp.Elems = elems
	if sig != nil {
		cp.Sig = sig
	}
	return &cp
}

// declared is the package's anonymous struct with fields, in name order,
// declared the first time it is asked for.
func (a *anonStructs) declared(fields []*ir.StructField) *ir.StructDef {
	want := (&ir.StructDef{Fields: fields, Anon: true}).SymType()
	for _, sd := range a.interned {
		if sd.SymType().Equal(want) {
			return sd
		}
	}
	sd := &ir.StructDef{
		Name:   freshName(a.names, ir.AnonStructBaseName(fields)),
		Pkg:    a.pkg,
		Fields: fields,
		Anon:   true,
	}
	a.interned = append(a.interned, sd)
	a.pkg.Structs = append(a.pkg.Structs, sd)
	return sd
}

// retypeLiteral types an anonymous struct literal whose type names a type
// parameter by its own values, where every value's type is concrete. One
// still reading a T -- in a generic body kept whole -- is left as it is.
func retypeLiteral(lit *ir.StructLit) {
	sd := ir.AnonStructDef(lit.Type)
	if sd == nil || !ir.IsUninternedAnon(sd) || !lit.Type.HasTypeParam() {
		return
	}
	for _, fi := range lit.Fields {
		if fi.Value != nil {
			if inner, ok := fi.Value.(*ir.StructLit); ok {
				retypeLiteral(inner)
			}
		}
	}
	fields := make([]*ir.StructField, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		var got *ir.Type
		for _, fi := range lit.Fields {
			if fi.Spread {
				return
			}
			if fi.Name == f.Name && fi.Value != nil {
				got = fi.Value.ExprType()
			}
		}
		// A null says nothing about what it is the absence of.
		if got == nil || got.HasTypeParam() || got.Kind == ir.TypeDyn || got.Kind == ir.TypeInvalid || got.Kind == ir.TypeNull {
			return
		}
		fields = append(fields, &ir.StructField{Name: f.Name, Type: got})
	}
	lit.Def = &ir.StructDef{Pkg: sd.Pkg, Fields: fields, Anon: true}
	lit.Type = lit.Def.SymType()
}
