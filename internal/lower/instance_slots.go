package lower

import (
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passInstanceSlots gives each instantiation that hands a runtime instance
// bare children a copy of the component with those children spliced in at the
// rest slot, and gives every runtime instance's remaining insertions their
// fallback. A factory or record is built from the declaration alone, so an
// insertion left in its body reaches the emitter with nothing to render.
//
// The children read the caller's scope, so they cross the way a slot child
// does in passSlotChildInstances: a value becomes a prop the render rewrites,
// and a handler an event whose body stays where it was written.
var passInstanceSlots = pass{
	name:    "InstanceSlots",
	enabled: hasInstanceRuntime,
	apply:   lowerInstanceSlots,
}

type instanceSlots struct {
	pkg   *ir.Package
	synth *slotChildSynth
	n     int
	// template is each runtime instance's body as the inliner left it, insertions
	// intact. A copy is taken from it rather than from the live body, which has
	// had its own insertions replaced by their fallbacks by the time a site
	// inside another declaration asks for one.
	template map[*ir.Component][]ir.Stmt
	queue    []*ir.Component
	// origin is the template node a copied node was cloned from, and copies
	// the copy each site has already been given, so a recursive body's copy
	// that meets its own site again reuses itself rather than copying forever.
	origin map[*ir.NodeInst]*ir.NodeInst
	copies map[copyKey]*ir.Component
	// droppable is every copy and every declaration a copy was taken of; one
	// no longer instantiated anywhere is dropped at the end.
	droppable map[*ir.Component]bool
}

type copyKey struct {
	comp *ir.Component
	site *ir.NodeInst
}

func lowerInstanceSlots(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &instanceSlots{
		pkg:       pkg,
		synth:     &slotChildSynth{pkg: pkg, reactive: collectReactiveVars(pkg)},
		template:  map[*ir.Component][]ir.Stmt{},
		origin:    map[*ir.NodeInst]*ir.NodeInst{},
		copies:    map[copyKey]*ir.Component{},
		droppable: map[*ir.Component]bool{},
	}
	for _, c := range pkg.Components {
		if c != nil && c.RuntimeInstance && hasRealComponentBody(c) {
			st.template[c] = deepCloneStmts(c.Body)
			st.queue = append(st.queue, c)
		}
	}
	for _, o := range ir.Owners(pkg) {
		if o.Comp == nil || !o.Comp.RuntimeInstance {
			st.specializeIn(o.Stmts())
		}
	}
	for len(st.queue) > 0 {
		c := st.queue[0]
		st.queue = st.queue[1:]
		if _, original := st.template[c]; original {
			c.Body = substituteSlots(c.Body, nil)
		}
		st.specializeIn(c.Body)
	}
	st.dropUnreferenced()
	return nil
}

func (st *instanceSlots) specializeIn(root any) {
	var sites []*ir.NodeInst
	_ = ir.Walk(root, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && wantsCopy(inst) {
			sites = append(sites, inst)
		}
		return nil
	})
	for _, n := range sites {
		st.specialize(n)
	}
}

func wantsCopy(n *ir.NodeInst) bool {
	c := n.Component
	return c != nil && c.RuntimeInstance && hasRealComponentBody(c) && len(n.Children) > 0 && c.RestSlot() != nil && !ir.IsWindowNode(n)
}

func (st *instanceSlots) specialize(n *ir.NodeInst) {
	orig := n.Component
	local := declaredWithin(n.Children)
	key := copyKey{orig, st.originOf(n)}
	if copyOf, ok := st.copies[key]; ok {
		// The children are a clone of the ones the copy was made from, so
		// lifting them names the props that copy already declares, in order.
		scratch := &ir.Component{}
		st.synth.liftHandlers(n.Children, scratch, n)
		st.synth.liftValues(n.Children, scratch, n, local)
		n.Children = nil
		n.Component = copyOf
		return
	}
	body, ok := st.template[orig]
	if !ok {
		body = orig.Body
	}
	copyOf := cloneComponent(orig, orig.Name+"__slot"+strconv.Itoa(st.n), body)
	st.n++
	st.copies[key] = copyOf
	st.recordOrigins(body, copyOf.Body)
	st.synth.liftHandlers(n.Children, copyOf, n)
	st.synth.liftValues(n.Children, copyOf, n, local)
	copyOf.Body = substituteSlots(copyOf.Body, n)
	n.Children = nil
	n.Component = copyOf

	st.droppable[orig] = true
	st.droppable[copyOf] = true
	st.queue = append(st.queue, copyOf)
	at := slices.Index(st.pkg.Components, orig)
	st.pkg.Components = slices.Insert(st.pkg.Components, at+1, copyOf)
	for i, f := range orig.Funcs {
		if slices.Contains(st.pkg.Funcs, f) {
			st.pkg.Funcs = append(st.pkg.Funcs, copyOf.Funcs[i])
		}
	}
}

func (st *instanceSlots) originOf(n *ir.NodeInst) *ir.NodeInst {
	if o, ok := st.origin[n]; ok {
		return o
	}
	return n
}

// recordOrigins pairs each node of a copied body with the one it was cloned
// from; the clone has the template's shape, so the two walks meet the nodes in
// the same order.
func (st *instanceSlots) recordOrigins(from, to []ir.Stmt) {
	nodes := func(stmts []ir.Stmt) []*ir.NodeInst {
		var out []*ir.NodeInst
		_ = ir.Walk(stmts, func(n ir.Node) error {
			if inst, ok := n.(*ir.NodeInst); ok {
				out = append(out, inst)
			}
			return nil
		})
		return out
	}
	src, dst := nodes(from), nodes(to)
	for i := range min(len(src), len(dst)) {
		st.origin[dst[i]] = st.originOf(src[i])
	}
}

// declaredWithin is every binding the content declares itself, which a lift
// must leave alone: only what it reads from the caller crosses.
func declaredWithin(stmts []ir.Stmt) map[ir.Symbol]bool {
	out := map[ir.Symbol]bool{}
	_ = ir.Walk(stmts, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.For:
			if x.KeySym != nil {
				out[x.KeySym] = true
			}
			if x.ValueSym != nil {
				out[x.ValueSym] = true
			}
		case *ir.LocalVar:
			if x.Sym != nil {
				out[x.Sym] = true
			}
		case *ir.NodeInst:
			if x.Handle != nil {
				out[x.Handle] = true
			}
		case *ir.Lambda:
			if x.Func != nil {
				for _, p := range x.Func.Params {
					out[p] = true
				}
			}
		}
		return nil
	})
	return out
}

// cloneComponent copies comp under name with state, props and methods of its
// own, so reactivity and the instance record see two declarations.
func cloneComponent(comp *ir.Component, name string, body []ir.Stmt) *ir.Component {
	out := *comp
	out.Name = name
	out.RuntimeInstance = true
	syms := map[ir.Symbol]ir.Symbol{}

	out.Vars = make([]*ir.Var, len(comp.Vars))
	for i, v := range comp.Vars {
		cv := cloneVarShallow(v)
		cv.Init = deepCloneExpr(v.Init)
		out.Vars[i] = cv
		syms[v] = cv
	}
	out.Props = make([]*ir.Prop, len(comp.Props))
	for i, p := range comp.Props {
		cp := *p
		cp.Default = deepCloneExpr(p.Default)
		if p.Sym != nil {
			sym := *p.Sym
			cp.Sym = &sym
			syms[p.Sym] = &sym
		}
		out.Props[i] = &cp
	}
	out.Funcs = make([]*ir.Func, len(comp.Funcs))
	for i, f := range comp.Funcs {
		cf := cloneFuncShallow(f)
		cf.Block = deepCloneStmts(f.Block)
		if cf.Receiver == comp.Name {
			cf.Receiver = name
		}
		out.Funcs[i] = cf
		syms[f] = cf
	}
	if comp.Methods != nil {
		out.Methods = make(map[string]*ir.Func, len(comp.Methods))
		for k, f := range comp.Methods {
			if cf, ok := syms[f].(*ir.Func); ok {
				out.Methods[k] = cf
			} else {
				out.Methods[k] = f
			}
		}
	}
	out.Events = slices.Clone(comp.Events)

	for _, v := range out.Vars {
		v.Init = renameInExpr(v.Init, nil, syms)
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = renameIdents(h.Func.Block, nil, syms)
			}
		}
	}
	for _, p := range out.Props {
		p.Default = renameInExpr(p.Default, nil, syms)
	}
	for _, f := range out.Funcs {
		f.Block = renameIdents(f.Block, nil, syms)
	}
	out.Body = renameIdents(deepCloneStmts(body), nil, syms)
	return &out
}

// dropUnreferenced removes every copy, and every declaration a copy was taken
// of, that nothing reachable from the program instantiates. Reachability rather
// than a reference count, because a recursive copy instantiates itself.
func (st *instanceSlots) dropUnreferenced() {
	live := map[*ir.Component]bool{}
	var queue []*ir.Component
	visit := func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			if inst, ok := n.(*ir.NodeInst); ok && inst.Component != nil && !live[inst.Component] {
				live[inst.Component] = true
				queue = append(queue, inst.Component)
			}
			return nil
		})
	}
	for _, o := range ir.Owners(st.pkg) {
		if o.Comp == nil || !st.droppable[o.Comp] {
			visit(o.Stmts())
		}
	}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		visit(c.Body)
	}
	dead := map[*ir.Func]bool{}
	st.pkg.Components = slices.DeleteFunc(st.pkg.Components, func(c *ir.Component) bool {
		if !st.droppable[c] || live[c] {
			return false
		}
		for _, f := range c.Funcs {
			dead[f] = true
		}
		return true
	})
	st.pkg.Funcs = slices.DeleteFunc(st.pkg.Funcs, func(f *ir.Func) bool { return dead[f] })
}
