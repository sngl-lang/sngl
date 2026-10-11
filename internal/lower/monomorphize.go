package lower

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// passMonomorphize gives each instantiation of a generic function a copy of
// its own, named for its type arguments and declared beside the original, and
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
// recursion would otherwise make forever. A call past it keeps the original.
const maxInstances = 64

type monomorph struct {
	slot      map[*ir.Func]*[]*ir.Func
	instances map[*ir.Func]map[string]*ir.Func
	kept      map[*ir.Func]bool
	pending   []*ir.Func
}

func lowerMonomorphize(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	m := &monomorph{
		slot:      map[*ir.Func]*[]*ir.Func{},
		instances: map[*ir.Func]map[string]*ir.Func{},
		kept:      map[*ir.Func]bool{},
	}
	inside := map[ir.Node]bool{}
	note := func(funcs *[]*ir.Func) {
		for _, fn := range *funcs {
			if isGenericBody(fn) {
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
	if len(m.slot) == 0 {
		return nil
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
	for fn, funcs := range m.slot {
		if len(m.instances[fn]) > 0 && !m.kept[fn] {
			*funcs = slices.DeleteFunc(slices.Clone(*funcs), func(f *ir.Func) bool { return f == fn })
		}
	}
	return nil
}

func isGenericBody(fn *ir.Func) bool {
	return fn != nil && len(fn.TypeParams)+len(fn.RecvTypeParams) > 0 &&
		fn.Intrinsic == "" && fn.Foreign.Name == "" && len(fn.Block) > 0
}

func (m *monomorph) visit(n ir.Node) {
	switch x := n.(type) {
	case *ir.Call:
		if _, ok := m.slot[x.Func]; !ok {
			return
		}
		b := map[string]*ir.Type{}
		for i, p := range x.Func.Params {
			if i < len(x.Args) && x.Args[i].Value != nil {
				ir.UnifyTypeParams(p.Type, x.Args[i].Value.ExprType(), b)
			}
		}
		ir.UnifyTypeParams(x.Func.Return, x.Type, b)
		if inst := m.instance(x.Func, b); inst != nil {
			x.Func = inst
		}
	case *ir.Ident:
		fn, ok := x.Sym.(*ir.Func)
		if !ok {
			return
		}
		if _, ok := m.slot[fn]; !ok || x.Type == nil || x.Type.Sig == nil {
			return
		}
		b := map[string]*ir.Type{}
		for i, p := range fn.Params {
			if i < len(x.Type.Sig.Params) {
				ir.UnifyTypeParams(p.Type, x.Type.Sig.Params[i].Type, b)
			}
		}
		ir.UnifyTypeParams(fn.Return, x.Type.Sig.Return, b)
		if inst := m.instance(fn, b); inst != nil {
			x.Sym = inst
			x.Name = inst.Name
		}
	}
}

// instance is fn's copy under b, made the first time it is asked for, or nil
// where b leaves a parameter unbound and the reference keeps the original.
func (m *monomorph) instance(fn *ir.Func, b map[string]*ir.Type) *ir.Func {
	params := append(slices.Clone(fn.RecvTypeParams), fn.TypeParams...)
	var key []string
	for _, tp := range params {
		t, ok := b[tp.Name]
		if !ok {
			m.kept[fn] = true
			return nil
		}
		key = append(key, typeKey(t))
	}
	k := strings.Join(key, "_")
	if inst, ok := m.instances[fn][k]; ok {
		return inst
	}
	if len(m.instances[fn]) >= maxInstances {
		m.kept[fn] = true
		return nil
	}
	inst := ir.InstantiateFunc(fn, b)
	inst.Name = fn.Name + "__" + k
	if m.instances[fn] == nil {
		m.instances[fn] = map[string]*ir.Func{}
	}
	m.instances[fn][k] = inst
	funcs := m.slot[fn]
	i := slices.Index(*funcs, fn)
	*funcs = slices.Insert(slices.Clone(*funcs), i+len(m.instances[fn]), inst)
	m.pending = append(m.pending, inst)
	return inst
}

// typeKey spells t as an identifier fragment: `list<int>` is `list_int`.
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
