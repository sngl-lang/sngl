package lower

import (
	"maps"
	"slices"
	"strconv"

	"duckfam.us/sngl/ir"
)

// passHandleBlocks makes a block a component of its own where it holds a
// node and every call of the node's methods through its `#id`, and the block
// is one the target may build at run time: a branch of an `if` or a `match`,
// a loop's body, or a population of a data component -- a tab's panel, which
// its tabs build where they show it.
// Built at run time, the node is an instance of its own, and a handler
// beside it calling `ask.refresh()` is the owner's function, which has no
// one instance to call it on (refuseUnsplicedHandleCalls); in a component
// with the node, the call is the component's own, as it is in a component a
// program writes around the two -- what the Pokédex's `asked` was for.
//
// The block is lifted as passSlotChildInstances lifts a slot child: what it
// reads from outside becomes a prop, and a handler becomes an event whose
// body stays at the site -- except a handler calling a method of a node in
// the block, which stays with the node. A write such a handler makes of what
// the block does not declare -- `failure = null` ahead of `ask.refresh()` --
// is raised to the site, which makes it (relayWrites); one whose place reads
// what the block binds, `rows[i] = x`, cannot be, and the block is left as it
// is, refused as before.
//
// After PropBindings, so a two-way binding is a value and a write-back
// handler the lift moves apart; before InlinePure and NoInlineComponents,
// which splice the component or build it at run time.
var passHandleBlocks = pass{
	name:    "HandleBlocks",
	enabled: func(Features) bool { return true },
	apply:   lowerHandleBlocks,
}

// lowerHandleBlocks reads InstanceState rather than being gated on it: only a
// target keeping an instance's state builds a block at run time, and bubbletea,
// which splices every copy, already calls the method each splice holds.
func lowerHandleBlocks(pkg *ir.Package, caps Features, _ Options) error {
	if pkg == nil || !caps.InstanceState {
		return nil
	}
	st := &slotChildSynth{pkg: pkg, reactive: collectReactiveVars(pkg), cells: map[ir.Symbol]bool{}, owned: ownedFuncs(pkg)}
	for _, c := range pkg.Components {
		for _, p := range c.Props {
			st.cells[p.Sym] = true
		}
	}
	callers := handleCallers(pkg)
	if len(callers) == 0 {
		return nil
	}
	hb := &handleBlocks{st: st, callers: callers}
	for i := 0; i < len(pkg.Components); i++ {
		if c := pkg.Components[i]; c != nil {
			hb.walk(&c.Stmts, false, c.Family)
		}
	}
	hb.walk(&pkg.Body, false, nil)
	return nil
}

type handleBlocks struct {
	st *slotChildSynth
	// callers is, for each handle a method is called through, the handlers
	// making the calls, and how many reads of the handle there are in all.
	callers map[*ir.Var]*handleUse
}

type handleUse struct {
	handlers map[*ir.Func]bool
	reads    int
}

// handleCallers is each node handle a method is called through.
func handleCallers(pkg *ir.Package) map[*ir.Var]*handleUse {
	out := map[*ir.Var]*handleUse{}
	eachHandler(pkg, func(fn *ir.Func) {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if c, ok := n.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == "" {
				if h, _ := handleReceiver(c); h != nil {
					use(out, h).handlers[fn] = true
				}
			}
			return nil
		})
	})
	reads := handleReads(pkg)
	for h, u := range out {
		u.reads = reads[h]
	}
	return out
}

// eachHandler calls f with the function of each handler under n: a node's, a
// boundary's, and a callback a node is handed as a prop, as android's
// primitives take theirs.
func eachHandler(n any, f func(*ir.Func)) {
	_ = ir.Walk(n, func(node ir.Node) error {
		switch x := node.(type) {
		case *ir.NodeInst:
			for _, h := range x.Handlers {
				if h.Func != nil {
					f(h.Func)
				}
			}
			for _, p := range x.Props {
				if l, ok := p.Value.(*ir.Lambda); ok && l.Func != nil {
					f(l.Func)
				}
			}
		case *ir.ErrorBoundary:
			if x.Handler != nil && x.Handler.Func != nil {
				f(x.Handler.Func)
			}
		}
		return nil
	})
}

// handleReads counts the reads of each node handle under n.
func handleReads(n any) map[*ir.Var]int {
	out := map[*ir.Var]int{}
	_ = ir.Walk(n, func(node ir.Node) error {
		if id, ok := node.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
				out[v]++
			}
		}
		return nil
	})
	return out
}

func use(m map[*ir.Var]*handleUse, h *ir.Var) *handleUse {
	u := m[h]
	if u == nil {
		u = &handleUse{handlers: map[*ir.Func]bool{}}
		m[h] = u
	}
	return u
}

// walk wraps each block under stmts that holds a called handle and all its
// callers, innermost first. wrappable says stmts is such a block itself, and
// fam is the family of the component it is written in, which the block's
// own component is a member of.
func (hb *handleBlocks) walk(stmts *[]ir.Stmt, wrappable bool, fam *ir.Component) {
	for _, s := range *stmts {
		_, branch := s.(*ir.If)
		_, arm := s.(*ir.Match)
		_, loop := s.(*ir.For)
		// A data component's population is a component value its record
		// holds, built where the record's owner shows it: a tab's panel.
		n, node := s.(*ir.NodeInst)
		held := node && ir.IsDataMember(n.Component)
		for _, b := range ir.ViewBlocks(s) {
			hb.walk(b, branch || arm || loop || held, fam)
		}
	}
	if !wrappable || len(*stmts) == 0 {
		return
	}
	kept, handles := hb.keptHandlers(*stmts)
	if kept == nil {
		return
	}
	*stmts = []ir.Stmt{hb.synthesize(*stmts, kept, handles, fam)}
}

// keptHandlers is the handlers of stmts that call a method of a node stmts
// holds, and the handles they call through, where stmts holds every read of
// such a handle and none of those handlers writes a binding stmts does not
// declare; nil where there is none, or one does.
func (hb *handleBlocks) keptHandlers(stmts []ir.Stmt) (map[*ir.Func]bool, map[*ir.Var]bool) {
	handles := map[*ir.Var]bool{}
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok {
			// A wrapper passImplicitState put in the node's place holds
			// the node, and the handle with it.
			for _, h := range []*ir.Var{n.Handle, n.Wraps} {
				if h != nil && hb.callers[h] != nil {
					handles[h] = true
				}
			}
		}
		return true
	})
	if len(handles) == 0 {
		return nil, nil
	}
	reads := handleReads(stmts)
	handlers := map[*ir.Func]bool{}
	eachHandler(stmts, func(fn *ir.Func) { handlers[fn] = true })
	local := declaredWithin(stmts)
	kept := map[*ir.Func]bool{}
	for h := range handles {
		u := hb.callers[h]
		if reads[h] != u.reads {
			return nil, nil
		}
		for fn := range u.handlers {
			if !handlers[fn] || !relayable(fn, local) {
				return nil, nil
			}
			kept[fn] = true
		}
	}
	return kept, handles
}

// synthesize lifts stmts into a component of its own and answers with the
// instantiation that takes their place, the handlers of kept left inside
// with their calls through handles.
func (hb *handleBlocks) synthesize(stmts []ir.Stmt, kept map[*ir.Func]bool, handles map[*ir.Var]bool, fam *ir.Component) *ir.NodeInst {
	st := hb.st
	comp := &ir.Component{Name: "__block" + strconv.Itoa(st.n), Family: fam}
	if fam == nil {
		comp.Family = blockFamily(stmts)
	}
	st.n++
	inst := &ir.NodeInst{Name: comp.Name, Component: comp}
	l := newLift(comp, inst)
	inner := varsWithin(stmts)
	_ = ir.Walk(stmts, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			if isDrawShape(x) {
				return nil
			}
			for i := range x.Handlers {
				if !kept[x.Handlers[i].Func] {
					liftHandler(&x.Handlers[i], l, nil, inner)
				}
			}
		case *ir.ErrorBoundary:
			if x.Handler != nil && !kept[x.Handler.Func] {
				liftHandler(x.Handler, l, nil, inner)
			}
		}
		return nil
	})
	local := declaredWithin(stmts)
	for fn := range kept {
		relayWrites(fn, l, local)
		for _, p := range fn.Params {
			local[p] = true
		}
	}
	body := slices.Clone(stmts)
	st.heldHandles = handles
	st.liftValues(body, l, handlerParams(body, local))
	st.heldHandles = nil
	comp.Stmts = body
	st.pkg.Components = append(st.pkg.Components, comp)
	return inst
}

// blockFamily is the family of what stmts renders: the one node's, or the
// family every node is a member of where they are several.
func blockFamily(stmts []ir.Stmt) *ir.Component {
	var fam *ir.Component
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		n, ok := s.(*ir.NodeInst)
		if !ok {
			return true
		}
		if fam == nil {
			fam = nodeFamily(n)
		}
		return false
	})
	return fam
}

// relayable reports whether each write fn makes of what the block does not
// declare can be raised to the site to make: one whose place names nothing
// the block or fn binds, `failure = null` and not `rows[i] = x`.
func relayable(fn *ir.Func, local map[ir.Symbol]bool) bool {
	bound := maps.Clone(local)
	maps.Copy(bound, declaredWithin(fn.Block))
	for _, p := range fn.Params {
		bound[p] = true
	}
	ok := true
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		target := placeWritten(n)
		if target == nil {
			return nil
		}
		if sym := exprRoot(target); sym != nil && liftableSym(sym) && !bound[sym] && readsAny(target, bound) {
			ok = false
			return ir.SkipAll
		}
		return nil
	})
	return ok
}

// relayWrites makes each write fn makes of what local does not declare a
// raise of an event of the lifted component, whose handler at the site makes
// it: the write lands where it was written, after what fn did before it and
// before what fn does next, since a raise runs its handler then and there.
func relayWrites(fn *ir.Func, l *lift, local map[ir.Symbol]bool) {
	bound := maps.Clone(local)
	maps.Copy(bound, declaredWithin(fn.Block))
	for _, p := range fn.Params {
		bound[p] = true
	}
	fn.Block = ir.RewriteBlocks(fn.Block, func(stmts []ir.Stmt) []ir.Stmt {
		for i, s := range stmts {
			target := placeWritten(s)
			if target == nil {
				continue
			}
			sym := exprRoot(target)
			if sym == nil || !liftableSym(sym) || bound[sym] {
				continue
			}
			name := "__on" + strconv.Itoa(l.events)
			l.events++
			site := &ir.Func{Block: []ir.Stmt{s}}
			emit := &ir.Emit{Name: name}
			if a, ok := s.(*ir.Assign); ok {
				p := &ir.Param{Name: name + "_v", Type: a.Target.ExprType()}
				site.Params = []*ir.Param{p}
				site.Block = []ir.Stmt{&ir.Assign{AST: a.AST, Target: a.Target, Op: a.Op, Value: &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}}}
				emit.Args = []ir.CallArg{{Value: a.Value}}
			}
			l.comp.Events = append(l.comp.Events, &ir.EventDecl{Name: name, Params: site.Params})
			l.inst.Handlers = append(l.inst.Handlers, ir.EventHandler{Name: name, Func: site})
			stmts[i] = emit
		}
		return stmts
	})
}

// placeWritten is the place n writes, or nil where it writes none.
func placeWritten(n any) ir.Expr {
	switch x := n.(type) {
	case *ir.Assign:
		return x.Target
	case *ir.Toggle:
		return x.Target
	}
	return nil
}
