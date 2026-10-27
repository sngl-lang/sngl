package lower

import (
	"slices"

	"duckfam.us/sngl/ir"
)

// A write to page state reaches every live instance reading it.
//
// A component built at run time holds its nodes and render slots in a record
// of its own, which a write anywhere else has no name for (reachable). So each
// such component reading a page var v -- in its own body, or through an
// instance one of its slots holds -- carries a refresh for v: a setter with no
// prop behind it, `__set___page_<v>`, which runs the updates its own body
// would after writing v and hands the call on to every instance its slots
// hold that refreshes for v too. A function of the page, `__refreshPage_<v>`,
// starts it from the page's own slots, and every write of v calls that after
// its own updates. The instance reads v where it stands, so a handler of its
// own sees the value written, and what each slot's registry holds is what is
// reached: one destroyed has left it.
//
// The setter goes through UpdateComponent as a prop's does, so each target
// reaches it as it reaches any setter of an instance; the value handed is
// unused.

// pageRefresh is what passReactivity plans of the refreshes before it walks
// any body, and fills once every render slot exists.
type pageRefresh struct {
	// page is the page's function per var, and comps each component's setter.
	page  map[*ir.Var]*ir.Func
	comps map[*ir.Component]map[*ir.Var]*ir.Func
	// held is the instantiations of components built at run time each owner
	// holds, by owner: a component, or nil for the page. fixed is those at a
	// position of their own, reached by their handle; every other is kept in
	// the registry of the render slot that builds it.
	held  map[*ir.Component][]*ir.NodeInst
	fixed map[*ir.NodeInst]bool
	// order is the vars in the order the package declares them.
	order []*ir.Var
}

// pageRefreshKey is the setter key a refresh for v is reached by.
func pageRefreshKey(v *ir.Var) string { return "__page_" + v.Name }

// planPageRefresh decides which components refresh for which page vars and
// declares their setters and the page's functions, empty: a write anywhere
// calls the page's, so it has to exist before the walk that injects the call.
// The page is the package body and, in a build rooted at a component, that
// component, whose state and registries are the page's.
func (st *reactivityState) planPageRefresh(root *ir.Component) {
	pr := &pageRefresh{
		page:  map[*ir.Var]*ir.Func{},
		comps: map[*ir.Component]map[*ir.Var]*ir.Func{},
		held:  map[*ir.Component][]*ir.NodeInst{},
		fixed: map[*ir.NodeInst]bool{},
	}
	st.refresh = pr
	page := map[*ir.Var]bool{}
	for _, v := range st.pkg.Vars {
		if st.reactiveVars[v] && !v.Synthesized {
			page[v] = true
			pr.order = append(pr.order, v)
		}
	}
	if len(page) == 0 {
		return
	}
	kept := map[*ir.Component]bool{}
	pr.held[nil] = st.heldInstances(st.pkg.Body)
	if root != nil {
		pr.held[nil] = append(pr.held[nil], st.heldInstances(root.Stmts)...)
	}
	for _, c := range st.pkg.Components {
		pr.held[c] = st.heldInstances(c.Stmts)
	}
	for _, ns := range pr.held {
		for _, n := range ns {
			kept[n.Component] = true
		}
	}
	// What each kept component's own body reads of the page.
	needs := map[*ir.Component]map[*ir.Var]bool{}
	reads := func(c *ir.Component, v *ir.Var) {
		if needs[c] == nil {
			needs[c] = map[*ir.Var]bool{}
		}
		needs[c][v] = true
	}
	for v := range page {
		for _, p := range st.reverseDeps[v] {
			if o, ok := p.owner.(compOwner); ok && kept[o.c] {
				reads(o.c, v)
			}
		}
		for _, s := range st.reverseSlots[v] {
			if o, ok := st.slotOwners[s.SlotID].(compOwner); ok && kept[o.c] {
				reads(o.c, v)
			}
		}
		for _, p := range st.reverseLive[v] {
			if o, ok := p.owner.(compOwner); ok && kept[o.c] {
				reads(o.c, v)
			}
		}
	}
	// And what an instance it holds reads, to a fixed point: a row inside a
	// panel inside the page is reached through the panel.
	for changed := true; changed; {
		changed = false
		for c := range kept {
			for _, n := range pr.held[c] {
				for v := range needs[n.Component] {
					if !needs[c][v] {
						reads(c, v)
						changed = true
					}
				}
			}
		}
	}
	for _, c := range st.pkg.Components {
		for _, v := range pr.order {
			if !needs[c][v] {
				continue
			}
			param := &ir.Param{Name: "__v", Type: ir.TypBool}
			f := &ir.Func{
				Name:        ir.ComponentSetter(pageRefreshKey(v)),
				Params:      []*ir.Param{param},
				Return:      ir.TypVoid,
				Purity:      ir.PurityMutates,
				Synthesized: true,
			}
			c.Funcs = append(c.Funcs, f)
			if pr.comps[c] == nil {
				pr.comps[c] = map[*ir.Var]*ir.Func{}
			}
			pr.comps[c][v] = f
		}
	}
	for _, v := range pr.order {
		if !slices.ContainsFunc(pr.held[nil], func(n *ir.NodeInst) bool { return needs[n.Component][v] }) {
			continue
		}
		f := &ir.Func{
			Name:        "__refreshPage_" + v.Name,
			Return:      ir.TypVoid,
			Purity:      ir.PurityMutates,
			Synthesized: true,
		}
		st.pkg.Funcs = append(st.pkg.Funcs, f)
		pr.page[v] = f
	}
}

// heldInstances is every instantiation of a component built at run time that
// stmts hold: one a render slot builds and keeps in a registry, at the top of
// the slot's body or under a node of it, and one at a position of its own --
// a recursion the build could not unroll -- which the owner names by its
// handle, given one here where it has none.
func (st *reactivityState) heldInstances(stmts []ir.Stmt) []*ir.NodeInst {
	var out []*ir.NodeInst
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		var body [][]ir.Stmt
		switch x := s.(type) {
		case *ir.NodeInst:
			if !isInstanceNode(x) {
				return true
			}
			if x.ID == "" {
				x.ID = st.freshNodeID()
			}
			st.refresh.fixed[x] = true
			out = append(out, x)
			return false
		case *ir.If:
			if x.LoweredSlotID != "" {
				body = [][]ir.Stmt{x.Body, x.Else}
			}
		case *ir.For:
			if x.LoweredSlotID != "" {
				body = [][]ir.Stmt{x.Body, x.Else}
			}
		}
		if body == nil {
			return true
		}
		for _, b := range body {
			ir.WalkView(b, func(s ir.Stmt) bool {
				if n, ok := s.(*ir.NodeInst); ok && isInstanceNode(n) {
					out = append(out, n)
					return false
				}
				return true
			})
		}
		return false
	})
	return out
}

// reaches reports whether the page's refresh for v reaches every instance of
// c: each site holding one is the page's, or a component's the refresh
// reaches in turn.
func (pr *pageRefresh) reaches(c *ir.Component, v *ir.Var) bool {
	if pr == nil || pr.page[v] == nil || pr.comps[c][v] == nil {
		return false
	}
	reached := map[*ir.Component]bool{}
	queue := []*ir.Component{nil}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		for _, n := range pr.held[h] {
			if k := n.Component; pr.comps[k][v] != nil && !reached[k] {
				reached[k] = true
				queue = append(queue, k)
			}
		}
	}
	for h, ns := range pr.held {
		if h != nil && !reached[h] && slices.ContainsFunc(ns, func(n *ir.NodeInst) bool { return n.Component == c }) {
			return false
		}
	}
	return reached[c]
}

// refreshCall is the call a write of v makes after its own updates, or nil
// where no instance the page holds reads v.
func (st *reactivityState) refreshCall(v *ir.Var) ir.Stmt {
	if st.refresh == nil || st.refresh.page[v] == nil {
		return nil
	}
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: st.refresh.page[v]}}
}

// fillPageRefresh writes each refresh's body, once every render slot and
// registry the walks made exists.
func (st *reactivityState) fillPageRefresh() {
	pr := st.refresh
	if pr == nil {
		return
	}
	saved := st.owner
	defer func() { st.owner = saved }()
	for _, c := range st.pkg.Components {
		for _, v := range pr.order {
			f := pr.comps[c][v]
			if f == nil {
				continue
			}
			st.owner = compOwner{c}
			own := func(o reactivityOwner) bool {
				oc, ok := o.(compOwner)
				return ok && oc.c == c
			}
			var props []reactiveProp
			for _, p := range st.reverseDeps[v] {
				if own(p.owner) {
					props = append(props, p)
				}
			}
			var slots []reactiveSlot
			for _, s := range st.reverseSlots[v] {
				if own(st.slotOwners[s.SlotID]) {
					slots = append(slots, s)
				}
			}
			var live []liveProp
			for _, p := range st.reverseLive[v] {
				if own(p.owner) {
					live = append(live, p)
				}
			}
			body := st.updaterStmts(props, slots, nil)
			if lu := st.liveUpdaters(live, slots, nil); len(lu) > 0 {
				n := len(body)
				for n > 0 && isSlotRefire(body[n-1]) {
					n--
				}
				body = append(body[:n:n], append(lu, body[n:]...)...)
			}
			f.Block = append(body, st.handOn(pr.held[c], compOwner{c}, v)...)
		}
	}
	for _, v := range pr.order {
		if f := pr.page[v]; f != nil {
			st.owner = pkgOwner{pkg: st.pkg, root: ir.AppParent}
			f.Block = st.handOn(pr.held[nil], st.owner, v)
		}
	}
}

// handOn calls the refresh for v of every instance the sites in held keep
// that has one. After the owner's own updates, slot re-fires included: a
// render reuses an instance without refreshing it, and one it builds afresh
// reads v as it stands.
func (st *reactivityState) handOn(held []*ir.NodeInst, owner reactivityOwner, v *ir.Var) []ir.Stmt {
	var out []ir.Stmt
	var live []liveProp
	for _, n := range held {
		if st.refresh.comps[n.Component][v] == nil {
			continue
		}
		if st.refresh.fixed[n] {
			out = append(out, &ir.CallStmt{Call: &ir.Call{
				Type: ir.TypVoid,
				Func: st.intrinsics[ir.NodeOpUpdateComponent],
				Args: []ir.CallArg{
					{Value: handleRef(n.ID, handleType(n))},
					{Value: &ir.Literal{Type: ir.TypString, Value: pageRefreshKey(v)}},
					{Value: &ir.Literal{Type: ir.TypBool, Value: "true"}},
				},
			}})
			continue
		}
		if st.sites[n] != nil {
			live = append(live, liveProp{node: n, key: pageRefreshKey(v), expr: &ir.Literal{Type: ir.TypBool, Value: "true"}, owner: owner})
		}
	}
	return append(out, st.liveUpdaters(live, nil, nil)...)
}
