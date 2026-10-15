package lower

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// A generic component built at run time is copied per instantiation on a
// target that does not emit a generic component (Features.GenericComponents),
// as passMonomorphize copies a function and monomorphizeStructs a struct:
// `show[int]` renders `show__int`, a declaration of its own whose props,
// events, slots, state, methods and body hold the types its call site bound.
// Emitted once with T erased, a `list[T]` prop was `[]any` on Go, which the
// call site's `[]int` is not, and `struct { v T }` had no spelling at all.
//
// The copy is made at the election, where the inliner decides an
// instantiation is built at run time, because that is the one place that
// knows: a spliced instantiation is already copied at its types by the
// splice, and a pass of its own ahead of this one would have to ask every
// question the election asks to find the same nodes. Everything after it
// reads the copy as an ordinary component: passInstanceSlots copies it again
// per content, a test reaching into it is refused as into any runtime
// instance, and android declares a composable per copy.

type componentInstance struct {
	args []*ir.Type
	comp *ir.Component
}

// instantiateRuntime points n, an instantiation elected to be built at run
// time, at its component's copy for the types it binds, making the copy the
// first time it is asked for. A component whose call site leaves a type
// parameter unbound -- a node in a body still generic -- keeps the original.
func (st *inlineCompState) instantiateRuntime(n *ir.NodeInst) error {
	c := n.Component
	if st.genericComponents || c == nil || len(c.TypeParams) == 0 {
		return nil
	}
	inst, err := st.componentInstance(n)
	if err != nil || inst == nil {
		return err
	}
	if n.Handle != nil {
		n.Handle.Type = retypeComponent(n.Handle.Type, c, inst)
	}
	n.Component = inst
	n.Name = inst.Name
	n.TypeArgs, n.WrittenTypeArgs = nil, nil
	return nil
}

func (st *inlineCompState) componentInstance(n *ir.NodeInst) (*ir.Component, error) {
	c := n.Component
	b := ir.ComponentBindings(n)
	if len(b) != len(c.TypeParams) {
		return nil, nil
	}
	args := make([]*ir.Type, len(c.TypeParams))
	key := make([]string, len(c.TypeParams))
	for i, tp := range c.TypeParams {
		args[i] = b[tp.Name]
		key[i] = typeKey(args[i])
	}
	for _, in := range st.compInsts[c] {
		if slices.EqualFunc(in.args, args, (*ir.Type).Equal) {
			return in.comp, nil
		}
	}
	if len(st.compInsts[c]) >= maxInstances {
		return nil, fmt.Errorf("%s: %q instantiates itself at a type it was not rendered at, or is rendered at more than %d types; this target writes a copy of a generic component per instantiation, and a recursion reaching a deeper type at every level needs one per level -- render it at the type it was given", nodePos(n), c.Name, maxInstances)
	}
	inst := ir.InstantiateComponent(c, b)
	inst.Name = freshName(st.declNames(), c.Name+"__"+strings.Join(key, "_"))
	st.compInsts[c] = append(st.compInsts[c], componentInstance{args, inst})
	at := slices.Index(st.pkg.Components, c)
	st.pkg.Components = slices.Insert(st.pkg.Components, at+len(st.compInsts[c]), inst)
	st.onList[inst] = true
	// The copy's state and methods are what the original's were: as reactive,
	// and holding what the points-to analysis said of them.
	for i, v := range c.Vars {
		if st.reactive[v] {
			st.reactive[inst.Vars[i]] = true
		}
		carryPointsTo(st.pkg, ir.SlotVarKey(v), ir.SlotVarKey(inst.Vars[i]))
	}
	for i, f := range c.Funcs {
		carryPointsTo(st.pkg, ir.SlotReturnKey(f), ir.SlotReturnKey(inst.Funcs[i]))
	}
	if st.cycles[c] {
		// A recursion is never walked by the inliner, so the copy's own
		// renders of the cycle are pointed at their copies here.
		st.cycles[inst] = true
		st.keep[inst] = true
		inst.RuntimeInstance = true
		var err error
		_ = ir.Walk(inst, func(nd ir.Node) error {
			if x, ok := nd.(*ir.NodeInst); ok && err == nil && st.cycles[x.Component] {
				err = st.instantiateRuntime(x)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return inst, nil
}

// declNames is every name the package declares at its top level, which a
// copy's name must not take.
func (st *inlineCompState) declNames() map[string]bool {
	if st.taken == nil {
		st.taken = map[string]bool{}
		for _, c := range st.pkg.Components {
			st.taken[c.Name] = true
		}
		for _, f := range st.pkg.Funcs {
			st.taken[f.Name] = true
		}
		for _, sd := range st.pkg.Structs {
			st.taken[sd.Name] = true
		}
		for _, e := range st.pkg.Enums {
			st.taken[e.Name] = true
		}
	}
	return st.taken
}

// retypeComponent is t with every component type naming from naming to
// instead: a handle's type, under whatever list or option its count gave it.
func retypeComponent(t *ir.Type, from, to *ir.Component) *ir.Type {
	if t == nil {
		return nil
	}
	if t.Kind == ir.TypeComponent && t.Decl == ir.Symbol(from) {
		cp := *t
		cp.Decl = to
		cp.Elems = nil
		return &cp
	}
	return t.MapChildren(func(c *ir.Type) *ir.Type { return retypeComponent(c, from, to) })
}
