package lower

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// monomorphizeEnums copies a generic enum per instantiation, as
// monomorphizeStructs does a struct: `Result[int]` is a declaration
// `Result__int` whose payloads are at the bound types, and every type naming
// the instantiation names the copy. A library's enum is copied into the
// program, since its declaration is every check's. It runs on each side of the
// struct pass, so a payload naming a generic struct and a struct field naming
// a generic enum each find the other's copy.
func monomorphizeEnums(pkg *ir.Package, names map[string]bool) {
	for _, ed := range pkg.Enums {
		names[ed.Name] = true
	}
	me := &monoEnums{pkg: pkg, names: names, copies: map[*ir.EnumDef][]enumInstance{}}
	for _, ed := range pkg.Enums {
		if ed.InstanceOf != nil {
			me.copies[ed.InstanceOf] = append(me.copies[ed.InstanceOf], enumInstance{ed.InstanceArgs, ed})
		}
	}
	ir.RewriteTypes(pkg, me.rewrite)
}

type enumInstance struct {
	args []*ir.Type
	ed   *ir.EnumDef
}

type monoEnums struct {
	pkg    *ir.Package
	names  map[string]bool
	copies map[*ir.EnumDef][]enumInstance
}

func (me *monoEnums) rewrite(t *ir.Type) *ir.Type {
	if t == nil {
		return t
	}
	t = t.MapChildren(me.rewrite)
	if ed, ok := t.Decl.(*ir.EnumDef); ok && t.Kind == ir.TypeEnum && len(ed.TypeParams) > 0 &&
		len(t.Elems) == len(ed.TypeParams) && !t.HasTypeParam() {
		return me.instance(ed, t.Elems).SymType()
	}
	return t
}

// instance is ed's declaration at args, made the first time it is asked for.
func (me *monoEnums) instance(ed *ir.EnumDef, args []*ir.Type) *ir.EnumDef {
	for _, in := range me.copies[ed] {
		if slices.EqualFunc(in.args, args, (*ir.Type).Equal) {
			return in.ed
		}
	}
	b := map[string]*ir.Type{}
	key := make([]string, len(args))
	for i, tp := range ed.TypeParams {
		b[tp.Name] = args[i]
		key[i] = typeKey(args[i])
	}
	cp := *ed
	cp.TypeParams = nil
	cp.Frozen = false
	cp.Name = freshName(me.names, ed.Name+"__"+strings.Join(key, "_"))
	cp.Members = make([]*ir.EnumMember, len(ed.Members))
	cp.Methods = nil
	cp.InstanceOf, cp.InstanceArgs = ed, args
	in := &cp
	me.copies[ed] = append(me.copies[ed], enumInstance{args, in})
	for i, m := range ed.Members {
		mc := *m
		if m.Payload != nil {
			mc.Payload = me.rewrite(m.Payload.Substitute(b))
		}
		cp.Members[i] = &mc
	}
	i := slices.Index(me.pkg.Enums, ed)
	if i < 0 {
		me.pkg.Enums = append(me.pkg.Enums, in)
	} else {
		me.pkg.Enums = slices.Insert(me.pkg.Enums, i+len(me.copies[ed]), in)
	}
	return in
}

// passMonomorphizeEnums copies each generic enum per instantiation once more,
// after the inliners: a library component's body spliced at the types its
// call site bound -- async.query's `var r = fetch(k)`, a `Result[T]` -- names
// instantiations no type held when Monomorphize ran, which a target spelling
// a type at a declaration (Kotlin's `var r: …`, a sealed class's variant) has
// no declaration for. A copy the first run made is found, not made again.
var passMonomorphizeEnums = pass{
	name:    "MonomorphizeEnums",
	enabled: func(c Features) bool { return !c.GenericFuncs },
	apply: func(pkg *ir.Package, _ Features, _ Options) error {
		if pkg == nil {
			return nil
		}
		names := map[string]bool{}
		for _, fn := range pkg.Funcs {
			names[fn.Name] = true
		}
		for _, sd := range pkg.Structs {
			names[sd.Name] = true
		}
		monomorphizeEnums(pkg, names)
		return nil
	},
}
