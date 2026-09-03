package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passInstanceBodies flattens the body of every component that survives
// inlining, on the platforms that otherwise keep the declarative tree.
//
// html renders a tree: it walks the visual nodes and writes markup, and only
// the imperative fragments -- a slot render func, a handler -- go through
// WalkLowered. That works for everything the build can decide statically, and
// an instance is the one thing it cannot: a component under a dynamic `for` is
// built when the loop runs, as many times as it has elements, so its body has
// to be a sequence of node operations rather than markup. Without this the
// factory had a body of visual nodes and emitted nothing at all from it.
var passInstanceBodies = pass{
	name:    "InstanceBodies",
	enabled: hasInstanceRuntime,
	apply:   lowerInstanceBodies,
}

// hasInstanceRuntime reports whether the target builds a component instance as
// a record with its own state, rather than as a method on the one model.
//
// NoReactivity says the target's reactive slots are already node operations, so
// it has a translator that reads them; !NoDeclarative says nothing has
// flattened a component body yet. Together that is the target whose instances
// are allocated at run time and reached through a handle.
//
// Neither half alone. A target that keeps both -- bubbletea, android -- walks
// the tree itself and has no translator, so a flattened body reaches its
// renderer as a call to `lower.CreateNode` and it has nothing to make of one. A
// target that sets both -- fyne, gtk4 -- emits an instance as a method on the
// shared model, where per-instance state has nowhere to live: promoting a prop
// there moves a per-call parameter onto a field every recursion frame shares,
// which is worse than the parameter it replaced. Those two want the record, and
// do not have it yet.
//
// With no target at all there is nothing to render, which is the state an LSP
// or a format pass lowers in.
func hasInstanceRuntime(c Caps) bool { return c.NoReactivity && !c.NoDeclarative }

func lowerInstanceBodies(pkg *ir.Package, caps Caps, opts Options) error {
	if pkg == nil {
		return nil
	}
	root := rootComponent(pkg, opts)
	st := newDeclarativeState(pkg, caps)
	st.seedCounter(pkg)
	// A factory's nodes are created and attached one at a time, and its
	// handlers close over the call -- the same shape a slot body needs, and
	// for the same reason: neither is emitted once at a fixed position.
	st.inlineHandlers = true
	for _, comp := range pkg.Components {
		if comp == nil || comp == root || !comp.RuntimeInstance {
			continue
		}
		comp.Body = st.processStmts(comp.Body, &comp.Funcs)
	}
	return nil
}
