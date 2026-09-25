package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passSlotChildInstances makes a slot child the render can keep.
//
// A slot places its children rather than rebuilding them, where the platform
// can put a child at a position -- see slotPlacement. The test it walks is
// whether the node already at the cursor *is* this node, so it can only ever
// match something the render hands back the same handle for. A component
// instance is that, because the registry holds it. A plain node is built fresh
// every render, so the comparison never matches one and the whole optimisation
// degrades to remove-all-append-all: focus, selection and scroll go with the
// nodes, and appending one row to a list of a hundred touches a hundred and
// one.
//
// So the plain node becomes a component. The alternative was to retain the
// node itself, and the reason that does not work is what the handler closes
// over: a `@click` written inside `for var x = xs` captures `x`, so keeping the
// node keeps a handler answering for the row it was first built for. An
// instance has no such problem -- its handlers read prop cells that
// `__set_<prop>` writes, and the render writes them again every time.
//
// The synthesis says both halves in the vocabulary that already exists:
//
//   - a free value the subtree reads becomes a prop, so the body reads a cell
//     instead of a capture;
//   - a handler becomes an event, so its body stays in the render function
//     where it was written and travels in as the event's lambda. That is the
//     same route passInstanceEvents gives a declared event, and it is why the
//     handler cannot go stale: reuseOrCreate re-points it on every render.
//
// Runs before passInstanceEvents, which is what turns the events it declares
// into the props the render re-points.
var passSlotChildInstances = pass{
	name: "SlotChildInstances",
	// InsertBefore is what makes this worth doing: without it a slot tears its
	// children down and appends them all back, so an identity across renders is
	// one nothing asks about, and the registry that maintains it would be pure
	// overhead. A synthesized row holds no state of its own -- retention buys
	// exactly the placement match and nothing else.
	enabled: func(c Features) bool { return hasInstanceRuntime(c) && c.InsertBefore },
	apply:   lowerSlotChildInstances,
}

// slotChildSynth counts the components this pass has minted, so each has its
// own name and its events do not collide with another's.
type slotChildSynth struct {
	pkg      *ir.Package
	reactive map[*ir.Var]bool
	n        int
}

func lowerSlotChildInstances(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &slotChildSynth{pkg: pkg, reactive: collectReactiveVars(pkg)}
	// Rooted at the bodies a visual tree is written in. Reaching an
	// imperative body under one costs nothing: only a view body holds a
	// NodeInst, so a `for` in a handler has no top-level node to convert.
	for _, c := range pkg.Components {
		if c != nil {
			st.walk(c.Body)
		}
	}
	st.walk(pkg.Body)
	for _, w := range pkg.Windows {
		if w != nil {
			st.walk(w.Children)
		}
	}
	return nil
}

// walk finds the reactive `for`s and hands their bodies over.
//
// A `for` and not an `if`, because a `for` is where placement can pay: its body
// renders many siblings, and appending or reordering is the case the cursor was
// written for. An `if` renders one arm, whose children are walked against a
// prev list that is either empty or exactly them, so retaining them saves at
// most the one node that was going to be re-inserted anyway -- and it would
// cost a synthesized component, a record and a factory for what is very often a
// constant label. `if` bodies keep the rebuild.
func (st *slotChildSynth) walk(stmts []ir.Stmt) {
	_ = ir.Walk(stmts, func(n ir.Node) error {
		f, ok := n.(*ir.For)
		if !ok || !dependsOnReactiveVar(f.Iter, st.reactive) {
			return nil
		}
		st.convertChildren(f.Body)
		st.convertChildren(f.Else)
		return nil
	})
}

// convertChildren replaces each plain top-level node of a slot body with an
// instantiation of a component synthesized from it.
//
// Top-level only, and in place: a child of one of those nodes is built and
// attached by its parent, so it is never a thing the placement cursor compares
// and retaining it separately would buy nothing.
func (st *slotChildSynth) convertChildren(stmts []ir.Stmt) {
	for i, s := range stmts {
		n, ok := s.(*ir.NodeInst)
		if !ok || !st.convertible(n) {
			continue
		}
		stmts[i] = st.synthesize(n)
	}
}

// convertible reports whether a node is one this pass can lift into a
// component of its own.
//
// The question is exactly the one the slot emitter asks -- isInstanceNode --
// because this pass exists to move a node from that test's failing side to its
// passing one. A node already instantiating a component with a body of its own
// is one the registry holds already; everything else is a node the render
// rebuilds, whether it is a raw element or the platform's wildcard component
// standing in for one.
//
// A node whose subtree holds its own control flow is left alone: its body would
// become a reactive slot inside the synthesized component, which changes what
// re-renders and is a larger question than placement.
func (st *slotChildSynth) convertible(n *ir.NodeInst) bool {
	if n == nil || isInstanceNode(n) {
		return false
	}
	simple := true
	_ = ir.Walk(n, func(node ir.Node) error {
		switch node.(type) {
		case *ir.If, *ir.For, *ir.SlotInst, *ir.ErrorBoundary:
			simple = false
		}
		return nil
	})
	return simple
}

// synthesize lifts n into a component of its own and answers with the
// instantiation that takes its place.
func (st *slotChildSynth) synthesize(n *ir.NodeInst) *ir.NodeInst {
	id := st.n
	st.n++
	comp := &ir.Component{
		Name:            "__child" + strconv.Itoa(id),
		RuntimeInstance: true,
	}

	inst := &ir.NodeInst{AST: n.AST, Component: comp}

	// The handlers first, because lifting one takes an expression out of the
	// subtree and the free-value scan should not then see what it reads.
	st.liftHandlers(n, comp, inst)
	st.liftValues(n, comp, inst, nil)

	comp.Body = []ir.Stmt{n}
	st.pkg.Components = append(st.pkg.Components, comp)
	return inst
}

// liftHandlers turns every handler in the subtree into an event the component
// declares and the instantiation subscribes to.
//
// The body does not move into the component: it stays the block the program
// wrote, at the site it was written, so what it closes over is still in scope.
// What crosses is the subscription, and passInstanceEvents makes that a prop
// cell the render re-points -- which is the whole reason a handler on a
// retained row does not go stale.
func (st *slotChildSynth) liftHandlers(n any, comp *ir.Component, inst *ir.NodeInst) {
	k := 0
	_ = ir.Walk(n, func(node ir.Node) error {
		node, ok := node.(*ir.NodeInst)
		if !ok {
			return nil
		}
		host := node.(*ir.NodeInst)
		for i := range host.Handlers {
			h := &host.Handlers[i]
			if h.Func == nil || len(h.Func.Block) == 0 {
				continue
			}
			name := "__on" + strconv.Itoa(k)
			k++
			comp.Events = append(comp.Events, &ir.EventDecl{Name: name})
			inst.Handlers = append(inst.Handlers, ir.EventHandler{
				Name: name,
				Func: h.Func,
			})
			// What is left on the node inside the component is the emit: the
			// host event still fires, and firing it is what calls out.
			h.Func = &ir.Func{Block: []ir.Stmt{&ir.Emit{Name: name}}}
		}
		return nil
	})
}

// liftValues turns every free value the subtree reads into a prop.
//
// Free means declared outside the node: the loop variable, and any of the
// owner's state the body names. Both are safe to read through a cell, because
// a slot re-renders whenever anything its body reads changes -- not only when
// the iterable does -- so the render is always the one pushing the new value.
//
// A call is a free value too, and the reason is not obvious: `decorate()`
// names none of the owner's state and still reads it, through the body of the
// func it calls. Leaving such a call inside the synthesized component leaves
// the read with it, and passReactivity then credits the owner's var with a
// node that now lives behind a component boundary -- the owner's updater
// patches `__nN` through the child's prop cell, and neither name exists in the
// owner's scope. Lifting the call restores the property the whole synthesis
// rests on: the child's subtree reads nothing but its own props. That is also
// what makes registerSlotBodyDeps enough on its own -- the call is a prop
// expression in the slot body now, so the slot re-fires when the func's reads
// change, and no second guard is needed for this boundary.
func (st *slotChildSynth) liftValues(n any, comp *ir.Component, inst *ir.NodeInst, local map[ir.Symbol]bool) {
	seen := map[string]*ir.Param{}
	add := func(key string, typ *ir.Type, value ir.Expr) *ir.Param {
		if p, known := seen[key]; known {
			return p
		}
		p := &ir.Param{Name: "__p" + strconv.Itoa(len(seen)), Type: typ}
		seen[key] = p
		comp.Props = append(comp.Props, &ir.Prop{Name: p.Name, Type: p.Type, Sym: p})
		inst.Props = append(inst.Props, ir.Arg{Name: p.Name, Value: value})
		return p
	}
	uniq := 0
	_ = ir.Rewrite(n, func(node ir.Node) (ir.Node, error) {
		switch x := node.(type) {
		case *ir.Call:
			typ := x.ExprType()
			if typ == nil || typ.Kind == ir.TypeVoid || !st.readsReactiveState(x) {
				return node, nil
			}
			key, ok := exprKey(x)
			if !ok {
				// Undecidable equality: give it a key of its own rather than
				// merge it with a call that may compute something else.
				key = "call#" + strconv.Itoa(uniq)
				uniq++
			}
			p := add(key, typ, x)
			// SkipDir: the call travels to the instantiation site whole, so
			// its arguments stay written against the scope they were read in.
			return &ir.Ident{Name: p.Name, Type: typ, Sym: p}, ir.SkipDir
		case *ir.Ident:
			if x.Sym == nil || !liftableSym(x.Sym) || local[x.Sym] {
				return node, nil
			}
			p := add("id("+identityOf(x.Sym)+")", x.Type, &ir.Ident{
				// The read as the site wrote it, before this walk repoints it.
				Name: x.Name, Type: x.Type, Sym: x.Sym,
			})
			x.Name = p.Name
			x.Sym = p
			return node, nil
		}
		return node, nil
	})
}

// readsReactiveState reports whether calling c reads state the owner holds --
// through the callee's body, and through everything that body calls in turn.
func (st *slotChildSynth) readsReactiveState(c *ir.Call) bool {
	if c.Func == nil {
		return false
	}
	reads := map[*ir.Var]bool{}
	gatherFuncReads(c.Func, st.reactive, reads, map[*ir.Func]bool{})
	return len(reads) > 0
}

// liftableSym reports whether a symbol read inside a node is one declared
// outside it. A func or a component named in an expression is neither state
// nor a capture, so neither becomes a prop.
func liftableSym(s ir.Symbol) bool {
	switch v := s.(type) {
	case *ir.LoopVar:
		return true
	case *ir.Param:
		return true
	case *ir.Var:
		return !v.IsConst
	}
	return false
}
