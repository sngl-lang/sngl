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
// It runs on every target, after every pass that copies a body at bound types
// -- passMonomorphize, and the inliners splicing a generic component: a target
// keeping a generic function whole still sees a call's result at the bound
// type. An anonymous
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
	// merged is each interned declaration another of equal fields stands for:
	// a directory-imported package interns its own `struct { v int }`, which
	// the program's output declares beside the program's under one name.
	merged map[*ir.StructDef]*ir.StructDef
}

func lowerAnonStructs(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	a := &anonStructs{pkg: pkg, names: map[string]bool{}, merged: map[*ir.StructDef]*ir.StructDef{}}
	for _, sd := range pkg.Structs {
		if !sd.Anon {
			a.names[sd.Name] = true
			continue
		}
		if first := a.find(sd.Fields); first != nil {
			a.merged[sd] = first
			continue
		}
		a.names[sd.Name] = true
		a.interned = append(a.interned, sd)
	}
	if len(a.merged) > 0 {
		pkg.Structs = slices.DeleteFunc(pkg.Structs, func(sd *ir.StructDef) bool { return a.merged[sd] != nil })
	}
	ir.RewriteTypes(pkg, a.rewrite)
	// A literal names its declaration as well as its type.
	_ = ir.Walk(pkg, func(n ir.Node) error {
		lit, ok := n.(*ir.StructLit)
		if !ok || lit.Def == nil || lit.Type == nil || !(ir.IsUninternedAnon(lit.Def) || a.merged[lit.Def] != nil) {
			return nil
		}
		if sd, ok := lit.Type.Decl.(*ir.StructDef); ok {
			lit.Def = sd
		}
		return nil
	})
	return nil
}

// rewrite is t with every anonymous struct in it the package's one
// declaration of those fields -- a concrete un-interned one's, or the first
// of several interned ones' -- t where there is nothing to replace.
func (a *anonStructs) rewrite(t *ir.Type) *ir.Type {
	if t == nil {
		return t
	}
	t = t.MapChildren(a.rewrite)
	sd := ir.AnonStructDef(t)
	switch {
	case sd == nil:
	case a.merged[sd] != nil:
		return a.merged[sd].SymType()
	case ir.IsUninternedAnon(sd) && !t.HasTypeParam():
		return a.declared(sd.Fields).SymType()
	}
	return t
}

// find is the interned declaration with fields, nil for none.
func (a *anonStructs) find(fields []*ir.StructField) *ir.StructDef {
	want := (&ir.StructDef{Fields: fields, Anon: true}).SymType()
	for _, sd := range a.interned {
		if sd.SymType().Equal(want) {
			return sd
		}
	}
	return nil
}

// declared is the package's anonymous struct with fields, in name order,
// declared the first time it is asked for.
func (a *anonStructs) declared(fields []*ir.StructField) *ir.StructDef {
	if sd := a.find(fields); sd != nil {
		return sd
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
