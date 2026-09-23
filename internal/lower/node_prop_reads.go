package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// passNodePropReads answers a prop read off a `#id` at build time, wherever the
// target keeps nothing to read it from at run time.
//
// `circle #dot(r=5.0)` beside `ui.text(value="{dot.r}")` becomes
// `ui.text(value="5.0")`. The shape is spliced into the calls that paint it
// before any backend sees it, so there is no node for `dot.r` to ask -- and
// asking anyway is what the Go targets did, emitting `m.dot.R` against a field
// nothing declares. It is not an approximation of the answer: with no object to
// mutate, what the prop was given *is* what the prop holds.
//
// Which nodes those are is the tree's to say, not this pass's. A primitive
// claims `#[gen.renders(identity)]` when a `#id` on what it draws names
// something still there while the program runs, and a read of such a node is
// left alone for the platform to emit. Nothing claimed is nothing held, which
// is the tier's polarity and the answer that leaves a program working: a
// target that has not thought about it gets a value that was true at build
// time rather than a reference to nothing.
//
// Always on, because it is gated by the declaration rather than by the target:
// a build with no capability to read still has a tree that answers.
var passNodePropReads = pass{
	name:    "NodePropReads",
	enabled: func(Features) bool { return true },
	apply:   lowerNodePropReads,
}

func lowerNodePropReads(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	handles := nodeHandles(pkg)
	if len(handles) == 0 {
		return nil
	}
	// ir.Rewrite rather than walkPackage and a switch of this pass's own. A
	// read can sit anywhere an expression can -- the one that started this was
	// inside an interpolation, two levels into a node's prop -- and every
	// hand-rolled descent in this package is a list of the shapes its author
	// thought of. That one is the shared traversal, its kind coverage enforced
	// by a panic and its slot coverage by a test.
	//
	// Reads only. A *write* to a node's prop is refused by the checker, for
	// every target at once: a prop is what the tree says it is, and a write
	// beside it is a second source of truth the next render undoes. So nothing
	// reaches here to answer, and this pass has no left-hand side to be careful
	// about.
	return ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		if sel, ok := n.(*ir.Select); ok {
			if e := rewriteNodePropRead(sel, handles); e != nil {
				return e, ir.SkipDir
			}
		}
		return n, nil
	})
}

// nodeHandles maps each `#id` binding to the node it names.
//
// Before passInlinePure, which is the whole reason this pass sits where it
// does: that pass carries a call site's ID and Handle onto the first node of
// the override body, so afterwards the handle names the *primitive* -- whose
// props are its own `@draw` or widget contract, not the `r=5.0` the program
// wrote. The props this pass reads exist only while the handle still points at
// the declaration the program named.
func nodeHandles(pkg *ir.Package) map[*ir.Var]*ir.NodeInst {
	out := map[*ir.Var]*ir.NodeInst{}
	for _, o := range ir.Owners(pkg) {
		collectNodeHandles(o.Stmts(), out)
	}
	return out
}

func collectNodeHandles(stmts []ir.Stmt, out map[*ir.Var]*ir.NodeInst) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ir.NodeInst:
			if v.Handle != nil {
				out[v.Handle] = v
			}
			collectNodeHandles(v.Children, out)
		case *ir.If:
			collectNodeHandles(v.Body, out)
			collectNodeHandles(v.Else, out)
		case *ir.For:
			collectNodeHandles(v.Body, out)
			collectNodeHandles(v.Else, out)
		case *ir.ErrorBoundary:
			collectNodeHandles(v.Children, out)
			collectNodeHandles(v.Failed, out)
		case *ir.ContextProvider:
			collectNodeHandles(v.Children, out)
		}
	}
}

// rewriteNodePropRead is what `handle.prop` should become, or nil to leave it
// alone: the expression the prop was given, for a handle whose node the target
// keeps nothing of.
func rewriteNodePropRead(sel *ir.Select, handles map[*ir.Var]*ir.NodeInst) ir.Expr {
	node := handleNodeOf(sel.Operand, handles)
	if node == nil || !rendersWithoutIdentity(node) {
		return nil
	}
	v := nodePropExpr(node, sel.Field)
	if v == nil {
		return nil
	}
	// Cloned: one prop read twice would otherwise share an expression node, and
	// a later pass rewriting one occurrence would rewrite both. Sharing the
	// *declarations* it names is the point -- a prop reading an enum member
	// still names that member.
	//
	// The cost of answering a read this way is that a non-trivial prop
	// expression is evaluated once per read rather than once. passCSE cannot
	// help: it is statement-local and imperative-only, and a view body has
	// nowhere to hold the temp. Accepted -- a prop whose value is expensive
	// enough to notice is already being evaluated once for the node itself.
	return ir.CloneExprSharingDecls(v)
}

// handleNodeOf is the node an expression names through its `#id`, or nil.
func handleNodeOf(e ir.Expr, handles map[*ir.Var]*ir.NodeInst) *ir.NodeInst {
	id, ok := e.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok || !v.NodeHandle {
		return nil
	}
	return handles[v]
}

// nodePropExpr is what a node's prop was given: the call site's argument, or
// the declaration's default where the call site left it out.
func nodePropExpr(n *ir.NodeInst, name string) ir.Expr {
	for i := range n.Props {
		if n.Props[i].Name == name {
			return n.Props[i].Value
		}
	}
	if n.Component == nil {
		return nil
	}
	for _, p := range n.Component.Props {
		if p.Name != name {
			continue
		}
		if p.Default != nil {
			return p.Default
		}
		// What a stdlib prop with no written default means, and the same
		// answer shapeBody gives when it splices a body.
		return ir.DeclaredDefault(p.Type)
	}
	return nil
}

// rendersWithoutIdentity reports whether this target is known to render n as
// something nothing can hold.
//
// *Known* is the load-bearing word, and it is why this is not simply
// `!ir.HoldsIdentity(...)`. A component with no override for the target being
// built renders no primitive at all, and so answers nil -- which is "I cannot
// tell", not "no identity". Reading the two the same way made a restricted
// harness, one that registers a single platform and merges no other's
// overrides, refuse a perfectly ordinary write to a `ui.text` handle.
//
// So an unresolved node is left exactly as it is: the transform applies where
// the tree positively says the node is gone, and nowhere else.
func rendersWithoutIdentity(n *ir.NodeInst) bool {
	prim := ir.RenderedPrimitive(n.Component)
	return prim != nil && !ir.HoldsIdentity(prim)
}

func identName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return id.Name
	}
	return "?"
}
