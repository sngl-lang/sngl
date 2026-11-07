package lower

import (
	"maps"
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
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
// the block or fn binds, `failure = null` and not `rows[i] = x`, made by an
// assignment, a toggle or a call mutating its receiver (`log.push(7)`). A
// call writing a place through a `ref` argument is none of those, and the
// block is left as it is.
func relayable(fn *ir.Func, local map[ir.Symbol]bool) bool {
	bound := boundIn(fn, local)
	ok := true
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		for _, place := range placesWritten(n) {
			if relayed(place, bound) && readsAny(place, bound) {
				ok = false
			}
		}
		if c, isCall := n.(*ir.Call); isCall && mutatingCallReceiver(c) == nil {
			for _, a := range c.Args {
				if u, isRef := a.Value.(*ir.Unary); isRef && u.Op == ast.UnaryAddr && relayed(u.Operand, bound) {
					ok = false
				}
			}
		}
		if !ok {
			return ir.SkipAll
		}
		return nil
	})
	return ok
}

// boundIn is what fn's body binds beside what the block declares.
func boundIn(fn *ir.Func, local map[ir.Symbol]bool) map[ir.Symbol]bool {
	bound := maps.Clone(local)
	maps.Copy(bound, declaredWithin(fn.Block))
	for _, p := range fn.Params {
		bound[p] = true
	}
	return bound
}

// relayed reports whether a write of place is one relayWrites raises: of
// state the block does not bind.
func relayed(place ir.Expr, bound map[ir.Symbol]bool) bool {
	sym := exprRoot(place)
	return sym != nil && liftableSym(sym) && !bound[sym]
}

// relayWrites makes each write fn makes of what local does not declare a
// raise of an event of the lifted component, whose handler at the site makes
// it: the write lands where it was written, after what fn did before it and
// before what fn does next, since a raise runs its handler then and there.
//
// What the raise hands on is evaluated in the block, so a write of a whole
// var hands on the value it writes, held in a local the rest of the block
// reads: the site's write reaches the block's prop only when the site renders
// again, which on android is its next recomposition, so `tries += 1; seen =
// tries` read the value before. A call mutating its receiver hands on its
// arguments, and the site makes the call.
func relayWrites(fn *ir.Func, l *lift, local map[ir.Symbol]bool) {
	bound := boundIn(fn, local)
	fn.Block = ir.RewriteBlocks(fn.Block, func(stmts []ir.Stmt) []ir.Stmt {
		wrote := map[ir.Symbol]*ir.Var{}
		out := make([]ir.Stmt, 0, len(stmts))
		for _, s := range stmts {
			readWrote(s, wrote)
			raise := func(params []*ir.Param, site []ir.Stmt, args []ir.CallArg) *ir.Emit {
				name := "__on" + strconv.Itoa(l.events)
				l.events++
				l.comp.Events = append(l.comp.Events, &ir.EventDecl{Name: name, Params: params})
				l.inst.Handlers = append(l.inst.Handlers, ir.EventHandler{Name: name, Func: &ir.Func{Params: params, Block: site}})
				return &ir.Emit{Name: name, Args: args}
			}
			param := func(name string, t *ir.Type) (*ir.Param, *ir.Ident) {
				p := &ir.Param{Name: name, Type: t}
				return p, &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}
			}
			switch x := s.(type) {
			case *ir.Assign:
				if !relayed(x.Target, bound) {
					break
				}
				name := "__on" + strconv.Itoa(l.events)
				p, read := param(name+"_v", x.Target.ExprType())
				id, whole := x.Target.(*ir.Ident)
				if !whole {
					out = append(out, raise([]*ir.Param{p}, []ir.Stmt{&ir.Assign{AST: x.AST, Target: x.Target, Op: x.Op, Value: read}}, []ir.CallArg{{Value: x.Value}}))
					delete(wrote, exprRoot(x.Target))
					continue
				}
				cur := ir.Expr(id)
				if w := wrote[id.Sym]; w != nil {
					cur = &ir.Ident{Name: w.Name, Type: w.Type, Sym: w, Synthesized: true}
				}
				tmp := &ir.Var{Name: name + wroteMark, Type: p.Type, Synthesized: true}
				out = append(out,
					&ir.LocalVar{Name: tmp.Name, Type: tmp.Type, Init: computeAssignNewVal(cur, x.Op, x.Value, p.Type), Sym: tmp},
					raise([]*ir.Param{p}, []ir.Stmt{&ir.Assign{AST: x.AST, Target: x.Target, Value: read}}, []ir.CallArg{{Value: &ir.Ident{Name: tmp.Name, Type: tmp.Type, Sym: tmp, Synthesized: true}}}))
				wrote[id.Sym] = tmp
				continue
			case *ir.Toggle:
				if !relayed(x.Target, bound) {
					break
				}
				out = append(out, raise(nil, []ir.Stmt{s}, nil))
				delete(wrote, exprRoot(x.Target))
				continue
			case *ir.CallStmt:
				recv := mutatingCallReceiver(x.Call)
				if recv == nil || !relayed(recv, bound) {
					break
				}
				call := *x.Call
				call.Args = slices.Clone(call.Args)
				var params []*ir.Param
				var args []ir.CallArg
				for i := 1; i < len(call.Args); i++ {
					p, read := param("__on"+strconv.Itoa(l.events)+"_"+strconv.Itoa(i), call.Args[i].Value.ExprType())
					params = append(params, p)
					args = append(args, ir.CallArg{Value: call.Args[i].Value})
					call.Args[i].Value = read
				}
				out = append(out, raise(params, []ir.Stmt{&ir.CallStmt{Call: &call}}, args))
				delete(wrote, exprRoot(recv))
				continue
			}
			out = append(out, s)
		}
		return out
	})
}

// readWrote points s's reads of each var wrote holds a local for at that
// local. A write's place is no read.
func readWrote(s ir.Stmt, wrote map[ir.Symbol]*ir.Var) {
	if len(wrote) == 0 {
		return
	}
	targets := map[ir.Expr]bool{}
	_ = ir.Walk(s, func(n ir.Node) error {
		for _, t := range placesWritten(n) {
			targets[t] = true
		}
		return nil
	})
	_ = ir.Walk(s, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && !targets[id] {
			if w := wrote[id.Sym]; w != nil {
				id.Name, id.Sym, id.Synthesized = w.Name, w, true
			}
		}
		return nil
	})
}

// placesWritten is each place n writes: an assignment's or a toggle's
// target, or the receiver of a call mutating it.
func placesWritten(n any) []ir.Expr {
	if c, ok := n.(*ir.Call); ok {
		if r := mutatingCallReceiver(c); r != nil {
			return []ir.Expr{r}
		}
		return nil
	}
	if t := placeWritten(n); t != nil {
		return []ir.Expr{t}
	}
	return nil
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
