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
// NoReactivity is the whole of the question: the target's reactive slots are
// already node operations, so it has an intrinsic translator that reads them,
// and an instance is a handle that translator can hold, re-point and destroy.
// That is html, fyne and gtk4 -- the three that emit a record of some kind, a
// closure on the one and a struct on the other two.
//
// A target that keeps its reactivity -- bubbletea, android -- walks the tree
// itself and has no translator, so a flattened body would reach its renderer
// as a call to `lower.CreateNode` and it would have nothing to make of one.
// Compose answers the question in its own vocabulary instead: a composable
// remembers its own state per call site, which is what a record is for.
//
// NoDeclarative used to be the other half, excluding fyne and gtk4 -- not
// because they keep the tree, but because neither had anywhere to put a
// per-instance cell: an instance was a method on the one Model, so promoting a
// prop moved a per-call parameter onto a field every row and every recursion
// frame shared. They have the record now (see each platform's
// emitComponentInstance), so the exclusion went with it.
//
// With no target at all there is nothing to render, which is the state an LSP
// or a format pass lowers in.
func hasInstanceRuntime(c Caps) bool { return c.NoReactivity }

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
