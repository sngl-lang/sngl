package codegen

import (
	"sort"

	"git.duckfam.us/jonathan/sngl/ir"
)

// collectUsedEnums returns the package's own enums followed by every library
// enum something the package declares is typed by: a var, a const, a field, a
// parameter or a result. pkg.Enums holds only the first, so a backend that
// declares one type per enum declared nothing for `var w = ui.FontWeight.bold`,
// and the reference named whatever the host had in scope under that name.
//
// Declarations rather than every expression's type, because a style literal
// or a primitive's prop names a library enum too, and those a platform
// translates into its own vocabulary rather than storing. The library half is
// sorted by name, since its discovery order is not meaningful.
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
		if !ok || t.Kind != ir.TypeEnum || seen[ed] {
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
		vars(c.Vars)
		for _, f := range c.Funcs {
			fn(f)
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.LocalVar:
			add(x.Type)
		case *ir.Lambda:
			fn(x.Func)
		}
		return nil
	})
	sort.Slice(lib, func(i, j int) bool { return lib[i].Name < lib[j].Name })
	return append(out, lib...)
}
