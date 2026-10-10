package lower

import (
	"fmt"

	"duckfam.us/sngl/ir"
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

func lowerNodePropReads(pkg *ir.Package, _ Features, opts Options) error {
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
	//
	// A cycle first, because the substitution below would chase one forever.
	if err := refuseNodePropCycle(handles); err != nil {
		return err
	}

	// To a fixed point, because a prop may itself have been given a read of
	// another node's prop -- `circle #b(r=a.r)` -- so one answer uncovers the
	// next. A single pass answered only the reads whose node the traversal had
	// already reached, which made the output depend on where the reading
	// statement was written relative to the node: the same program rendered
	// `b is 5` with the read after the canvas and an empty span with it before.
	// A declarative position has no order to appeal to, so the answer must not
	// have one either.
	tests := testInstances(pkg, opts)
	for {
		changed := false
		if err := ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
			sel, ok := n.(*ir.Select)
			if !ok {
				return n, nil
			}
			e := rewriteNodePropRead(sel, handles)
			if e == nil {
				e = rewriteTestPropRead(sel, tests)
			}
			if e == nil {
				return n, nil
			}
			changed = true
			// Skipped, not descended: the replacement is answered by the next
			// round, where every node has been reached. Descending here would
			// answer it at a depth this round has no information about, which
			// is the order-dependence again one level down.
			return e, ir.SkipDir
		}); err != nil {
			return err
		}
		if !changed {
			return nil
		}
	}
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
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok && n.Handle != nil {
			out[n.Handle] = n
		}
		return true
	})
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

// refuseNodePropCycle reports a node prop that reads its way back to itself.
//
// `circle #a(r=b.r)` beside `circle #b(r=a.r)` is a question with no answer:
// each prop is the other, and the fixed point above would substitute one into
// the other forever. A program can write it, so it is reported rather than
// hung on -- the same shape `evalCtx.foldingProp` guards for a window prop that
// reads itself, and reported here for the reason that one is not: this is a
// whole-graph question, and the graph is known.
//
// Only the reads this pass would answer are edges. A read of a node the target
// retains is left standing and is not one, so a widget reading another widget
// is not a cycle -- it is two live reads, whatever else is wrong with them.
func refuseNodePropCycle(handles map[*ir.Var]*ir.NodeInst) error {
	// The edge set: node -> the nodes its props read.
	edges := map[*ir.NodeInst][]*ir.NodeInst{}
	for _, node := range handles {
		if !rendersWithoutIdentity(node) {
			continue
		}
		for i := range node.Props {
			ir.WalkExprs(node.Props[i].Value, func(e ir.Expr) error {
				sel, ok := e.(*ir.Select)
				if !ok {
					return nil
				}
				if to := handleNodeOf(sel.Operand, handles); to != nil {
					edges[node] = append(edges[node], to)
				}
				return nil
			})
		}
	}

	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := map[*ir.NodeInst]int{}
	var walk func(*ir.NodeInst) error
	walk = func(n *ir.NodeInst) error {
		state[n] = onStack
		for _, to := range edges[n] {
			switch state[to] {
			case onStack:
				return fmt.Errorf("%s: `#%s` reads its own value back through `#%s`: each prop is the other, so there is nothing to answer with",
					ir.StmtPos(n), n.ID, to.ID)
			case unvisited:
				if err := walk(to); err != nil {
					return err
				}
			}
		}
		state[n] = done
		return nil
	}
	for node := range edges {
		if state[node] != unvisited {
			continue
		}
		if err := walk(node); err != nil {
			return err
		}
	}
	return nil
}

// testInstances is the component parameter of every test this build emits:
// the instance a test reaches what its component renders through. A build
// rooted at a component emits the tests handed it, and a build of the whole
// program emits every test or, where Options.Tests is off, none. A test the
// build does not emit is left as it was checked, so that nothing it names is
// kept alive for it.
func testInstances(pkg *ir.Package, opts Options) map[*ir.Param]bool {
	out := map[*ir.Param]bool{}
	if !opts.Tests {
		return out
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		for _, p := range fn.Params {
			if p.Type == nil || p.Type.Kind != ir.TypeComponent {
				continue
			}
			if c, ok := p.Type.Decl.(*ir.Component); ok && (opts.RootComponent == "" || c.Name == opts.RootComponent) {
				out[p] = true
			}
		}
	}
	return out
}

// rewriteTestPropRead answers a test's read of a node's one-way prop --
// `c.t.value`, `c.b.shown.value` -- with the expression the node's call site
// gave it, read through the instance the node belongs to. A one-way prop says
// what the node shows for as long as it is rendered, so that expression is
// what the node holds; and no target's test reaches the host widget to ask it,
// which spells the prop its own way, if it keeps it at all.
//
// What the expression reads of the component that renders the node is read
// through the instance (`hits` in bar's body is `c.b.hits` in the test), which
// passNoInlineComponents then repoints at the spliced copy as it repoints any
// other member a test reads. What it reads of something a test cannot name --
// a loop's variable, a slot's parameter -- has no value at the test, and such
// a read is left as it was.
func rewriteTestPropRead(sel *ir.Select, tests map[*ir.Param]bool) ir.Expr {
	inner, ok := sel.Operand.(*ir.Select)
	if !ok || !rootedAtTest(inner.Operand, tests) {
		return nil
	}
	comp := componentOf(inner.Operand)
	if comp == nil {
		return nil
	}
	node, always := ownNode(comp, inner.Field)
	if node == nil || node.Component == nil {
		return nil
	}
	for _, p := range node.Component.Props {
		if p.Name == sel.Field && p.Bidirectional {
			return nil
		}
	}
	v := nodePropExpr(node, sel.Field)
	if v == nil {
		return nil
	}
	// One rendered only some of the time has the expression's value only
	// while it is, and the interpreter answers nothing while it is not; a
	// read of it is left to the targets that keep the node.
	if !always {
		return nil
	}
	return rebaseOnInstance(ir.CloneExprSharingDecls(v), comp, inner.Operand)
}

// rootedAtTest reports whether e is a test's instance or a select through it.
func rootedAtTest(e ir.Expr, tests map[*ir.Param]bool) bool {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Ident:
			p, ok := x.Sym.(*ir.Param)
			return ok && tests[p]
		default:
			return false
		}
	}
}

func componentOf(e ir.Expr) *ir.Component {
	t := e.ExprType()
	if t == nil || t.Kind != ir.TypeComponent {
		return nil
	}
	c, _ := t.Decl.(*ir.Component)
	return c
}

// ownNode is the node comp's body renders under the id, or nil, and whether
// the body renders it always. One under a `for` is a copy per iteration, which
// a select names none of; one under an `if`, or in a page's content, is there
// only while the condition holds or the page shows.
func ownNode(comp *ir.Component, id string) (node *ir.NodeInst, always bool) {
	var walk func(stmts []ir.Stmt, looped, conditional bool)
	walk = func(stmts []ir.Stmt, looped, conditional bool) {
		for _, s := range stmts {
			if n, ok := s.(*ir.NodeInst); ok && n.ID == id && !looped && node == nil {
				node, always = n, !conditional
			}
			_, isFor := s.(*ir.For)
			_, isIf := s.(*ir.If)
			page := false
			if n, ok := s.(*ir.NodeInst); ok && n.Component != nil && n.Component.Builtin == ir.BuiltinNavPage {
				page = true
			}
			for _, b := range ir.ViewBlocks(s) {
				walk(*b, looped || isFor, conditional || isIf || page)
			}
		}
	}
	walk(comp.Stmts, false, false)
	return node, always
}

// rebaseOnInstance rewrites e, written in comp's body, to read comp's members
// through inst, or nil where e reads what a test cannot name.
func rebaseOnInstance(e ir.Expr, comp *ir.Component, inst ir.Expr) ir.Expr {
	own := map[ir.Symbol]bool{}
	for _, v := range comp.Vars {
		own[v] = true
	}
	for _, p := range comp.Props {
		if p.Sym != nil {
			own[p.Sym] = true
		}
	}
	ir.WalkView(comp.Stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok && n.Handle != nil {
			own[n.Handle] = true
		}
		return true
	})
	unnamed := false
	out := newExprWalker(func(x ir.Expr) ir.Expr {
		id, ok := x.(*ir.Ident)
		if !ok || id.Sym == nil {
			return x
		}
		if own[id.Sym] {
			return &ir.Select{Type: id.Type, Operand: ir.CloneExprSharingDecls(inst), Field: id.Name}
		}
		switch id.Sym.(type) {
		case *ir.Param, *ir.LoopVar:
			unnamed = true
		}
		return x
	}).expr(e)
	if unnamed {
		return nil
	}
	return out
}
