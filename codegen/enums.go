package codegen

import (
	"sort"

	"duckfam.us/sngl/ir"
)

// collectUsedEnums returns the package's own enums followed by every library
// enum the package's code has a value or a declaration of; Kotlin declares one
// class per enum.
//
// Two places name a library enum without the host ever spelling it, and are
// skipped: a literal of a library struct the package never declares -- a Style
// handed to style= -- and a member written directly as a node's prop. A
// platform reads both and translates them into its own vocabulary.
func collectUsedEnums(pkg *ir.Package) []*ir.EnumDef {
	if pkg == nil {
		return nil
	}
	seen := map[*ir.EnumDef]bool{}
	out := make([]*ir.EnumDef, 0, len(pkg.Enums))
	for _, e := range pkg.Enums {
		seen[e] = true
		out = append(out, e)
	}
	declared := map[*ir.StructDef]bool{}
	for _, s := range pkg.Structs {
		declared[s] = true
	}
	var lib []*ir.EnumDef
	var add func(t *ir.Type)
	add = func(t *ir.Type) {
		if t == nil {
			return
		}
		for _, el := range t.Elems {
			add(el)
		}
		if t.Sig != nil {
			for _, p := range t.Sig.Params {
				add(p.Type)
			}
			add(t.Sig.Return)
		}
		ed, ok := t.Decl.(*ir.EnumDef)
		if !ok || t.Kind != ir.TypeEnum || seen[ed] || ed.Foreign.Name != "" {
			return
		}
		seen[ed] = true
		lib = append(lib, ed)
	}
	fn := func(f *ir.Func) {
		if f == nil {
			return
		}
		for _, p := range f.Params {
			add(p.Type)
		}
		add(f.Return)
	}
	vars := func(vs []*ir.Var) {
		for _, v := range vs {
			add(v.Type)
		}
	}
	var slots func(ss []*ir.SlotDecl)
	slots = func(ss []*ir.SlotDecl) {
		for _, s := range ss {
			for _, p := range s.Params {
				add(p.Type)
			}
			slots(s.Slots)
		}
	}
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			add(f.Type)
		}
	}
	vars(pkg.Consts)
	vars(pkg.Vars)
	for _, f := range pkg.Funcs {
		fn(f)
	}
	for _, c := range pkg.Components {
		for _, p := range c.Props {
			add(p.Type)
		}
		slots(c.Slots)
		vars(c.Vars)
		for _, f := range c.Funcs {
			fn(f)
		}
	}
	propMembers := map[*ir.Ident]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			for _, a := range x.Props {
				if id, ok := a.Value.(*ir.Ident); ok && id.Member != "" {
					propMembers[id] = true
				}
			}
		case *ir.Ident:
			if propMembers[x] {
				return nil
			}
		case *ir.StructLit:
			if x.Def != nil && x.Def.Pkg != "" && !declared[x.Def] {
				return ir.SkipDir
			}
		case *ir.LocalVar:
			add(x.Type)
		case *ir.For:
			add(x.ElemType)
		case *ir.Lambda:
			fn(x.Func)
		}
		if e, ok := n.(ir.Expr); ok {
			add(e.ExprType())
		}
		return nil
	})
	sort.Slice(lib, func(i, j int) bool { return lib[i].Name < lib[j].Name })
	return append(out, lib...)
}
