package lower

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// passMonomorphize gives each instantiation of a generic function -- and of a
// generic struct, in monomorphizeStructs -- a copy of its own, named for its type arguments and declared beside the original, and
// points every call and reference at the copy. A target that cannot emit a
// type parameter then sees concrete types only: Go puts a function reading
// state on the Model, where no method may take one, and a body comparing two
// T's needs a constraint nothing in SNGL states.
//
// It runs after ReceiverParam and CallDefaults, so a call's arguments are
// every parameter's, and the bindings are read off them and off the call's
// own type, which is what binds a parameter only the result names.
var passMonomorphize = pass{
	name:    "Monomorphize",
	enabled: func(c Features) bool { return !c.GenericFuncs },
	apply:   lowerMonomorphize,
}

// maxInstances bounds the copies of one function, which polymorphic
// recursion would otherwise make forever. A reference past it keeps the
// original, and with it everything the original calls.
const maxInstances = 16

type instance struct {
	args []*ir.Type
	fn   *ir.Func
}

type monomorph struct {
	// pkg is the build's copy of the package, which notes PastInstances.
	pkg *ir.Package
	// lib is where a library generic copy says to copy is declared, nil where
	// a library's are left alone.
	lib       *[]*ir.Func
	copy      func(*ir.Func) bool
	slot      map[*ir.Func]*[]*ir.Func
	instances map[*ir.Func][]instance
	kept      map[*ir.Func]bool
	names     map[string]bool
	pending   []*ir.Func
}

// passMonomorphizeCodecs copies, on every target, each generic function a
// codec call reaches at one of its type parameters (ir.GenericCodecs): every
// language's codec reads the type at the call, which a body emitted once
// does not know. After the splice, since a generic component's body is
// spliced at its bound types only then, and the calls it makes are bound
// only from then.
//
// On a target that emits no type parameter it copies every library generic a
// spliced body calls, for passMonomorphize's reason: a generic override --
// a select's, calling `ui.choiceLabel(options, value)` -- is spliced at its
// call's types only here, after passMonomorphize has run. The copies, and the
// spliced bodies, name struct and enum instantiations passMonomorphize never
// saw, which are copied in turn. So does a program generic a generic
// component's body calls at the component's parameters -- a method of
// `Cache[V]` writing the cache a `:cache Cache[V]` prop names -- which
// passMonomorphize kept, the call naming V, and which Go then emitted taking
// the uncopied struct the spliced call no longer hands it. One it kept past
// maxInstances it leaves (PastInstances): copied again, a polymorphic
// recursion grew past the bound a second time
// (cmd/sngl/testdata/generic_method_in_generic_component_runs.txt).
var passMonomorphizeCodecs = pass{
	name:    "MonomorphizeCodecs",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, c Features, _ Options) error {
		var reach ir.CodecReach
		monomorphizeFuncs(pkg, func(fn *ir.Func) bool {
			return (!c.GenericFuncs && !pkg.PastInstances[fn]) || len(reach.GenericCodecs(fn)) > 0
		}, false)
		if !c.GenericFuncs && pkg != nil {
			names := declNames(pkg)
			monomorphizeEnums(pkg, names)
			monomorphizeStructs(pkg, names)
			monomorphizeEnums(pkg, names)
		}
		return nil
	},
}

// A library's generic is promoted only later, by LibFuncs, so the codec pass
// takes in one a call reaches and declares its copies among the program's.

// declNames is every function and struct name pkg declares, which a copy's
// name must not be.
func declNames(pkg *ir.Package) map[string]bool {
	names := map[string]bool{}
	for _, fn := range pkg.Funcs {
		names[fn.Name] = true
	}
	for _, c := range pkg.Components {
		for _, fn := range c.Funcs {
			names[fn.Name] = true
		}
	}
	for _, sd := range pkg.Structs {
		names[sd.Name] = true
	}
	return names
}

func lowerMonomorphize(pkg *ir.Package, _ Features, _ Options) error {
	monomorphizeFuncs(pkg, func(*ir.Func) bool { return true }, true)
	return nil
}

// monomorphizeFuncs copies each generic function copy says to per
// instantiation, and with types the struct and enum instantiations the
// copies name.
func monomorphizeFuncs(pkg *ir.Package, copy func(*ir.Func) bool, types bool) {
	if pkg == nil {
		return
	}
	m := &monomorph{
		pkg:       pkg,
		copy:      copy,
		slot:      map[*ir.Func]*[]*ir.Func{},
		instances: map[*ir.Func][]instance{},
		kept:      map[*ir.Func]bool{},
		names:     map[string]bool{},
	}
	inside := map[ir.Node]bool{}
	note := func(funcs *[]*ir.Func) {
		for _, fn := range *funcs {
			m.names[fn.Name] = true
			if isGenericBody(fn) && copy(fn) {
				m.slot[fn] = funcs
				_ = ir.Walk(fn, func(n ir.Node) error {
					inside[n] = true
					return nil
				})
			}
		}
	}
	note(&pkg.Funcs)
	for _, c := range pkg.Components {
		note(&c.Funcs)
	}
	for _, sd := range pkg.Structs {
		m.names[sd.Name] = true
	}
	// The structs after the functions: a function's copy names the struct
	// instantiations its bindings make, which the struct pass then finds.
	if !types {
		m.lib = &pkg.Funcs
	}
	if types {
		defer monomorphizeEnums(pkg, m.names)
		defer monomorphizeStructs(pkg, m.names)
		defer monomorphizeEnums(pkg, m.names)
	}
	if len(m.slot) == 0 && m.lib == nil {
		return
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if !inside[n] {
			m.visit(n)
		}
		return nil
	})
	for len(m.pending) > 0 {
		fn := m.pending[0]
		m.pending = m.pending[1:]
		_ = ir.Walk(fn, func(n ir.Node) error {
			m.visit(n)
			return nil
		})
	}
	m.keepCallees()
	for fn, funcs := range m.slot {
		if len(m.instances[fn]) > 0 && !m.kept[fn] {
			*funcs = slices.DeleteFunc(slices.Clone(*funcs), func(f *ir.Func) bool { return f == fn })
		}
	}
}

// keepCallees keeps every generic a kept original reaches: its body is
// emitted with its parameters unbound, and names the originals it calls.
func (m *monomorph) keepCallees() {
	var work []*ir.Func
	for fn := range m.kept {
		work = append(work, fn)
	}
	for len(work) > 0 {
		fn := work[len(work)-1]
		work = work[:len(work)-1]
		_ = ir.Walk(fn, func(n ir.Node) error {
			var callee *ir.Func
			switch x := n.(type) {
			case *ir.Call:
				callee = x.Func
			case *ir.Ident:
				callee, _ = x.Sym.(*ir.Func)
			}
			if _, ok := m.slot[callee]; ok && !m.kept[callee] {
				m.kept[callee] = true
				work = append(work, callee)
			}
			return nil
		})
	}
}

func isGenericBody(fn *ir.Func) bool {
	return fn != nil && len(fn.TypeParams)+len(fn.RecvTypeParams) > 0 &&
		fn.Intrinsic == "" && fn.Foreign.Name == "" && len(fn.Block) > 0 &&
		!boundByItsComponent(fn)
}

// boundByItsComponent reports whether fn is a method of a component generic
// in the component's parameters alone: the splice of each instance binds
// them (InstantiateFunc in expandCall), as InstantiateComponent does for a
// copy, so a copy per call here was one per type every splice then cloned --
// a select's trigger at the Fruit a test chose, in the int select's splice.
func boundByItsComponent(fn *ir.Func) bool {
	return fn.OnComponent() && len(fn.TypeParams) == 0
}

func (m *monomorph) visit(n ir.Node) {
	switch x := n.(type) {
	case *ir.Call:
		if _, ok := m.slot[x.Func]; !ok && !m.takeLib(x.Func) {
			return
		}
		_, b := ir.GenericCallee(x)
		if inst := m.instance(x.Func, b); inst != nil {
			x.Func = inst
			// A default CallDefaults wrote is typed by the declaration's T.
			for i := range x.Args {
				if t := x.Args[i].Value.ExprType(); t.HasTypeParam() {
					x.Args[i].Value = ir.SubstituteExprTypes(x.Args[i].Value, b)
				}
			}
		}
	case *ir.Ident:
		fn, ok := x.Sym.(*ir.Func)
		if !ok {
			return
		}
		if _, ok := m.slot[fn]; !ok {
			return
		}
		if x.Type == nil || x.Type.Sig == nil {
			m.kept[fn] = true
			return
		}
		b := map[string]*ir.Type{}
		for i, p := range fn.Params {
			if i < len(x.Type.Sig.Params) {
				ir.BindTypeParams(p.Type, x.Type.Sig.Params[i].Type, b)
			}
		}
		ir.BindTypeParams(fn.Return, x.Type.Sig.Return, b)
		if inst := m.instance(fn, b); inst != nil {
			x.Sym = inst
			x.Name = inst.Name
		}
	}
}

// takeLib slots fn where it is a library generic m copies.
func (m *monomorph) takeLib(fn *ir.Func) bool {
	if m.lib == nil || !isGenericBody(fn) || fn.PkgPath() == "" || !m.copy(fn) {
		return false
	}
	m.slot[fn] = m.lib
	return true
}

// instance is fn's copy under b, made the first time it is asked for, or nil
// where b leaves a parameter unbound and the reference keeps the original.
func (m *monomorph) instance(fn *ir.Func, b map[string]*ir.Type) *ir.Func {
	params := append(slices.Clone(fn.RecvTypeParams), fn.TypeParams...)
	args := make([]*ir.Type, len(params))
	var key []string
	for i, tp := range params {
		// A binding naming a type parameter is a call in a body still
		// generic, which keeps the original.
		t, ok := b[tp.Name]
		if !ok || t.HasTypeParam() {
			m.kept[fn] = true
			return nil
		}
		args[i] = t
		key = append(key, typeKey(t))
	}
	for _, in := range m.instances[fn] {
		if slices.EqualFunc(in.args, args, (*ir.Type).Equal) {
			return in.fn
		}
	}
	if len(m.instances[fn]) >= maxInstances {
		m.kept[fn] = true
		if m.pkg.PastInstances == nil {
			m.pkg.PastInstances = map[*ir.Func]bool{}
		}
		m.pkg.PastInstances[fn] = true
		return nil
	}
	inst := ir.InstantiateFunc(fn, b)
	inst.Name = m.fresh(fn.Name + "__" + strings.Join(key, "_"))
	m.instances[fn] = append(m.instances[fn], instance{args, inst})
	funcs := m.slot[fn]
	if i := slices.Index(*funcs, fn); i >= 0 {
		*funcs = slices.Insert(slices.Clone(*funcs), i+len(m.instances[fn]), inst)
	} else {
		*funcs = append(slices.Clone(*funcs), inst)
	}
	m.pending = append(m.pending, inst)
	return inst
}

// fresh is name, or name with a number where two instantiations spell
// alike: `func(int) string` and `func(int, string)` are both func_int_string.
func (m *monomorph) fresh(name string) string {
	return freshName(m.names, name)
}

// freshName is name, or name with a number where it is taken, and takes it.
func freshName(names map[string]bool, name string) string {
	out := name
	for n := 1; names[out]; n++ {
		out = fmt.Sprintf("%s_%d", name, n)
	}
	names[out] = true
	return out
}

// typeKey spells t as an identifier fragment: `list[int]` is `list_int`.
func typeKey(t *ir.Type) string {
	var b strings.Builder
	under := false
	for _, r := range t.String() {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			under = false
		} else if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
