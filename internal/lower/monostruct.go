package lower

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// monomorphizeStructs gives each instantiation of a generic struct a
// declaration of its own, `Box__int` for `Box[int]`, with its fields at the
// bound types, and points every type naming the instantiation at it. It is
// passMonomorphize's other half, for the same targets and the same reason: Go
// erased a generic struct's field to `any`, so `box.value + 1` added an int
// to an interface, and Kotlin declared it `Any = null`, which does not
// compile. A struct whose instantiation still names a type parameter -- in a
// generic function kept whole -- keeps the original, which is then still
// declared.
func monomorphizeStructs(pkg *ir.Package, names map[string]bool) {
	generic := map[*ir.StructDef]bool{}
	for _, sd := range pkg.Structs {
		if len(sd.TypeParams) > 0 && sd.Builtin == ir.BuiltinNone && sd.Foreign.Name == "" && !sd.Frozen {
			generic[sd] = true
		}
	}
	if len(generic) == 0 {
		return
	}
	ms := &monoStructs{pkg: pkg, generic: generic, names: names, copies: map[*ir.StructDef][]structInstance{}, copyOf: map[*ir.StructDef]*ir.StructDef{}}
	ir.RewriteTypes(pkg, ms.rewrite)
	if len(ms.copies) == 0 {
		return
	}
	// A literal names its declaration as well as its type.
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if lit, ok := n.(*ir.StructLit); ok && lit.Type != nil {
			if sd, ok := lit.Type.Decl.(*ir.StructDef); ok && lit.Def != sd && generic[lit.Def] {
				lit.Def = sd
			}
		}
		return nil
	})
	// A method's copy is a method of the struct's: its receiver, whose type
	// still names the generic declaration, is the instantiation its receiver
	// parameter was bound to. Left on the original, a target emitting a
	// method beside its type found no type to emit it beside.
	reattach := func(funcs []*ir.Func) {
		for _, fn := range funcs {
			if fn.Receiver == nil || len(fn.TypeParams)+len(fn.RecvTypeParams) > 0 {
				continue
			}
			orig, ok := fn.Receiver.Decl.(*ir.StructDef)
			if !ok || !generic[orig] {
				continue
			}
			recv := fn.RecvParam
			if recv == nil && len(fn.Params) > 0 {
				recv = fn.Params[0]
			}
			if recv == nil || recv.Type == nil {
				continue
			}
			if inst, ok := recv.Type.Decl.(*ir.StructDef); ok && ms.copyOf[inst] == orig {
				fn.Receiver = recv.Type
			}
		}
	}
	reattach(pkg.Funcs)
	for _, c := range pkg.Components {
		reattach(c.Funcs)
	}
	// An original nothing names any more is declared by nobody.
	still := map[*ir.StructDef]bool{}
	ir.RewriteTypes(pkg, func(t *ir.Type) *ir.Type {
		markStructs(t, generic, still)
		return nil
	})
	pkg.Structs = slices.DeleteFunc(pkg.Structs, func(sd *ir.StructDef) bool {
		return generic[sd] && len(ms.copies[sd]) > 0 && !still[sd]
	})
}

type structInstance struct {
	args []*ir.Type
	sd   *ir.StructDef
}

type monoStructs struct {
	pkg     *ir.Package
	generic map[*ir.StructDef]bool
	names   map[string]bool
	copies  map[*ir.StructDef][]structInstance
	copyOf  map[*ir.StructDef]*ir.StructDef
}

// rewrite is t with every concrete instantiation of a generic struct in it
// replaced by that instantiation's declaration, or t where there is none.
func (ms *monoStructs) rewrite(t *ir.Type) *ir.Type {
	if t == nil {
		return t
	}
	// An anonymous struct's fields are part of its type, and nothing else
	// reaches them: `struct { inner Box[int] }` names Box__int.
	if sd := ir.AnonStructDef(t); sd != nil && ir.IsUninternedAnon(sd) {
		var fields []*ir.StructField
		for i, f := range sd.Fields {
			ft := ms.rewrite(f.Type)
			if ft == f.Type && fields == nil {
				continue
			}
			if fields == nil {
				fields = slices.Clone(sd.Fields[:i])
			}
			fields = append(fields, &ir.StructField{Name: f.Name, Type: ft})
		}
		if fields == nil {
			return t
		}
		return (&ir.StructDef{Pkg: sd.Pkg, Fields: fields, Anon: true}).SymType()
	}
	elems, changed := ms.rewriteAll(t.Elems)
	if sd, ok := t.Decl.(*ir.StructDef); ok && t.Kind == ir.TypeStruct && ms.generic[sd] &&
		len(elems) == len(sd.TypeParams) && !slices.ContainsFunc(elems, (*ir.Type).HasTypeParam) {
		return ms.instance(sd, elems).SymType()
	}
	var sig *ir.FuncSig
	if t.Kind == ir.TypeFunc && t.Sig != nil {
		params := make([]*ir.Param, len(t.Sig.Params))
		for i, p := range t.Sig.Params {
			params[i] = p
			if nt := ms.rewrite(p.Type); nt != p.Type {
				cp := *p
				cp.Type = nt
				params[i] = &cp
				changed = true
			}
		}
		ret := ms.rewrite(t.Sig.Return)
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

func (ms *monoStructs) rewriteAll(ts []*ir.Type) ([]*ir.Type, bool) {
	out := ts
	changed := false
	for i, e := range ts {
		if ne := ms.rewrite(e); ne != e {
			if !changed {
				out = slices.Clone(ts)
				changed = true
			}
			out[i] = ne
		}
	}
	return out, changed
}

// instance is sd's declaration at args, made the first time it is asked for
// and declared right after sd.
func (ms *monoStructs) instance(sd *ir.StructDef, args []*ir.Type) *ir.StructDef {
	for _, in := range ms.copies[sd] {
		if slices.EqualFunc(in.args, args, (*ir.Type).Equal) {
			return in.sd
		}
	}
	b := map[string]*ir.Type{}
	key := make([]string, len(args))
	for i, tp := range sd.TypeParams {
		b[tp.Name] = args[i]
		key[i] = typeKey(args[i])
	}
	cp := *sd
	cp.TypeParams = nil
	cp.Name = freshName(ms.names, sd.Name+"__"+strings.Join(key, "_"))
	cp.Fields = make([]*ir.StructField, len(sd.Fields))
	cp.Methods = nil
	in := &cp
	// Recorded before the fields are rewritten, so a field naming the
	// instantiation it belongs to -- a tree of nodes -- finds this one.
	ms.copies[sd] = append(ms.copies[sd], structInstance{args, in})
	ms.copyOf[in] = sd
	for i, f := range sd.Fields {
		fc := *f
		fc.Type = ms.rewrite(f.Type.Substitute(b))
		if f.Default != nil {
			fc.Default = ir.SubstituteExprTypes(ir.CloneExprSharingDecls(f.Default), b)
		}
		cp.Fields[i] = &fc
	}
	i := slices.Index(ms.pkg.Structs, sd)
	ms.pkg.Structs = slices.Insert(ms.pkg.Structs, i+len(ms.copies[sd]), in)
	return in
}

// markStructs records which of generic t still names, anywhere in it.
func markStructs(t *ir.Type, generic, still map[*ir.StructDef]bool) {
	if t == nil {
		return
	}
	if sd, ok := t.Decl.(*ir.StructDef); ok && generic[sd] {
		still[sd] = true
	}
	for _, e := range t.Elems {
		markStructs(e, generic, still)
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			markStructs(p.Type, generic, still)
		}
		markStructs(t.Sig.Return, generic, still)
	}
}
