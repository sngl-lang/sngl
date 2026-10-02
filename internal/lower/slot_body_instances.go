package lower

import "git.duckfam.us/jonathan/sngl/ir"

// A render slot re-renders on what its structure reads -- an `if`'s
// condition, a `for`'s iterable -- and what its body reads beyond that is
// patched where it stands.
//
// A re-render builds the body afresh, so a slot that re-fired on everything
// its body read destroyed and rebuilt its whole subtree whenever one label in
// it changed: a panel under `if details` whose label reads a ticking clock was
// a new panel every tick, and lost whatever its widgets held that no state
// described -- focus, a selection, the text typed into an unbound input.
//
// The patch needs somewhere to reach the nodes the live render holds, one set
// of them per copy a `for` renders, and a component built at run time is that
// already: a record holding its widgets, a setter per prop writing the widgets
// that read it. So each top-level node of a slot body that reads state is
// lifted into one (synthesize, the lift the placement conversion below uses),
// and the slot keeps the instances across renders as it keeps any other
// (reuseOrCreate). What the lifted subtree read becomes a prop; a write to it
// hands every live instance the new value (passReactivity's registry
// updaters), which is how a prop of an instance outside a slot is written.
//
// A node reading nothing is left to the rebuild, which only a structural
// change now triggers: it would cost a component, a record and a factory for
// what is very often a constant label.

// liftSlotBodies finds the structures that will be render slots and lifts
// what their bodies read.
func (st *slotChildSynth) liftSlotBodies(stmts []ir.Stmt) {
	_ = ir.Walk(stmts, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.If:
			if st.readsCells(x.Cond) {
				st.liftArm(x.Body)
				st.liftArm(x.Else)
			}
		case *ir.For:
			// A loop is a slot when its iterable reads state, and when its body
			// does whatever it iterates: collectFromFor's two questions. A loop
			// of pages is no slot, which collectFromFor answers first.
			if loopsOverPages(x) {
				return nil
			}
			if st.readsCells(x.Iter) || st.armReadsCells(x.Body) || st.armReadsCells(x.Else) {
				st.liftArm(x.Body)
				st.liftArm(x.Else)
			}
		}
		return nil
	})
}

// liftArm lifts each top-level node of a slot body that reads state, reaching
// through an `if` or a `for` the slot renders structurally.
func (st *slotChildSynth) liftArm(stmts []ir.Stmt) {
	for i, s := range stmts {
		switch x := s.(type) {
		case *ir.NodeInst:
			if st.liftable(x) {
				stmts[i] = st.synthesize(x)
			}
		case *ir.If:
			if !st.readsCells(x.Cond) {
				st.liftArm(x.Body)
				st.liftArm(x.Else)
			}
		case *ir.For:
			if !st.readsCells(x.Iter) {
				st.liftArm(x.Body)
				st.liftArm(x.Else)
			}
		}
	}
}

// liftable reports whether n is a node this pass lifts: one the render builds
// afresh, whose subtree reads state, and which can be moved behind a component
// boundary without taking something with it that the rest of the program
// names.
//
//   - A component with a body is an instance already.
//   - A slot insertion inserts the slot of the component it is written in,
//     which the lifted component does not declare; a boundary's handler, where
//     it does something, and a canvas's drawing are bodies the lift has no
//     route for.
//   - A handle something reads would name a node that lives in the instance.
func (st *slotChildSynth) liftable(n *ir.NodeInst) bool {
	if n == nil || isInstanceNode(n) || !st.armReadsCells([]ir.Stmt{n}) {
		return false
	}
	ok := true
	_ = ir.Walk(n, func(node ir.Node) error {
		switch x := node.(type) {
		case *ir.SlotInst, *ir.ContextProvider:
			ok = false
		case *ir.ErrorBoundary:
			// One whose handler does nothing takes no body with it: a window's,
			// whose call site handled no @error, which every window's content
			// is under. A raise under it is caught where it is raised.
			if len(x.Failed) > 0 || x.Handler == nil || x.Handler.Func == nil || len(x.Handler.Func.Block) > 0 {
				ok = false
			}
		case *ir.NodeInst:
			if ir.IsShapeContainer(x) || isDrawShape(x) ||
				(x.Handle != nil && st.readHandles[x.Handle]) {
				ok = false
			}
		}
		if !ok {
			return ir.SkipAll
		}
		return nil
	})
	return ok
}

// armReadsCells reports whether a view body renders anything from state: a
// prop, a condition or an iterable reading a cell. Handlers are not rendered.
func (st *slotChildSynth) armReadsCells(stmts []ir.Stmt) bool {
	found := false
	slotBodyExprs(stmts, func(e ir.Expr) {
		found = found || st.readsCells(e)
	})
	return found
}

// readsCells reports whether e reads state: a var, a runtime instance's prop,
// or a call whose body reads a var.
func (st *slotChildSynth) readsCells(e ir.Expr) bool {
	if e == nil {
		return false
	}
	found := false
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		switch y := x.(type) {
		case *ir.Ident:
			if v, ok := y.Sym.(*ir.Var); ok && st.reactive[v] {
				found = true
			} else if y.Sym != nil && st.cells[y.Sym] {
				found = true
			}
		case *ir.Call:
			found = found || st.readsReactiveState(y)
		case *ir.Lambda, *ir.Closure:
			// Read when it is called, not when the view renders.
			return ir.SkipDir
		}
		if found {
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// runtimeProps is the parameter of every prop a component built at run time
// declares, root excepted: passComponentProps makes each a cell, so a view
// body reading one re-renders from it.
func runtimeProps(pkg *ir.Package, root *ir.Component) map[ir.Symbol]bool {
	out := map[ir.Symbol]bool{}
	for _, c := range pkg.Components {
		if c == nil || c == root || !c.RuntimeInstance {
			continue
		}
		for _, p := range c.Props {
			if p != nil && p.Sym != nil {
				out[p.Sym] = true
			}
		}
	}
	return out
}

// readNodeHandles is every `#id` handle an expression in the package reads.
func readNodeHandles(pkg *ir.Package) map[ir.Symbol]bool {
	out := map[ir.Symbol]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
				out[v] = true
			}
		}
		return nil
	})
	return out
}
