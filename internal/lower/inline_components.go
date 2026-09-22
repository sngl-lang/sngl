package lower

import (
	"fmt"
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passNoInlineComponents inlines every non-recursive, non-native, non-main
// user-defined component into main. After the pass, codegen only sees one
// real ir.Component (main) plus any recursive cycles. See
// docs/superpowers/specs/2026-05-16-component-inlining-design.md.
var passNoInlineComponents = pass{
	name:    "NoInlineComponents",
	enabled: func(c Caps) bool { return c.NoInlineComponents },
	apply:   lowerInlineComponents,
}

func lowerInlineComponents(pkg *ir.Package, _ Caps, opts Options) error {
	if pkg == nil {
		return nil
	}
	// main is nil for every ordinary build: a window is the root, and the
	// visual tree lives in pkg.Windows or in the package's own body. It is a
	// component only where a harness made one the whole program.
	main := rootComponent(pkg, opts)
	if main == nil && len(pkg.Windows) == 0 && len(pkg.Body) == 0 {
		// No root to inline into. Every component is its own entry point, so
		// there is nothing to flatten and nothing is unreachable -- running
		// the pass anyway would retain an empty keep set and drop them all.
		return nil
	}
	cycles := findRecursiveCycles(pkg, opts)
	reactive := collectReactiveVars(pkg)
	onList := make(map[*ir.Component]bool, len(pkg.Components))
	for _, c := range pkg.Components {
		onList[c] = true
	}
	st := &inlineCompState{pkg: pkg, main: main, cycles: cycles, reactive: reactive, platform: opts.Platform, local: opts.localComponents, onList: onList, instSeq: seqOrOwn(opts.instSeq), demoted: map[*ir.Func]bool{}}
	if err := st.run(); err != nil {
		return err
	}
	clearDemotedReceivers(pkg, st.demoted)
	dropNestedMethods(pkg, st.keep)
	pkg.Components = retainComponents(pkg.Components, st.keep)
	return uniqueNodeIDs(pkg)
}

// dropNestedMethods removes from pkg.Funcs the methods of every component the
// inliner is about to drop.
//
// The checker registers a nested method in both pkg.Funcs and its component's
// Funcs -- the same *ir.Func in each, which is why this matches by pointer.
// Dropping the declaration and leaving the members behind left every Go
// target emitting the original method of a component whose props no longer
// exist anywhere: each instance got its own clone with the arguments folded
// in, hoisted onto the owner it was inlined into, while the original still
// read the bare prop names and named nothing the Model declares.
//
// A component something instantiates at run time is kept, so its methods stay
// and the instance record emits them.
func dropNestedMethods(pkg *ir.Package, keep map[*ir.Component]bool) {
	dropped := make(map[*ir.Func]bool)
	for _, c := range pkg.Components {
		if keep[c] {
			continue
		}
		for _, f := range c.Funcs {
			dropped[f] = true
		}
	}
	if len(dropped) == 0 {
		return
	}
	out := pkg.Funcs[:0]
	for _, f := range pkg.Funcs {
		if !dropped[f] {
			out = append(out, f)
		}
	}
	pkg.Funcs = out
}

// uniqueNodeIDs makes each id name one node again.
//
// An id is the program's name for a node, and splicing a body puts a copy of
// every id in it into the caller -- so two instances of one component named one
// node twice. The callee's state is renamed per instance for exactly this
// reason; its ids were not, and a backend resolving the name answered for
// whichever node it reached first: two instances of a counter shared one
// element, and bumping either updated the same one.
//
// The first occurrence keeps the name. A program addresses a node by the name
// it wrote, and with a single instance -- overwhelmingly the common case --
// that name is unambiguous and must survive; a test naming `#inc` means the
// `#inc` there is. With several instances the program has no way to say which
// it meant, so the later ones are the ones that give up the name.
//
// Renaming here rather than while splicing, because "is this id ambiguous" is a
// question about the finished owner and not about any one instance: the splice
// that introduces a duplicate cannot tell it is doing so.
func uniqueNodeIDs(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	owners := ir.Owners(pkg)
	// The package's own names are held against every owner, not just against
	// the package. passHoistState moves a single-window program's state onto
	// the package while the nodes stay on the window, so the var and the id
	// that collide are reached under two different owners -- and html emits
	// both into one page regardless.
	shared := ownerNames(packageOwner(owners))
	for _, o := range owners {
		read := handleReads(o.Stmts())
		reads := handleIdents(o.Stmts())
		// The owner's own names are taken before any id is. A node id shares
		// one emitted namespace with the vars, consts and funcs the owner
		// declares -- html spells a state var `state.label` and a node handle
		// by its bare id, so `#label` beside `var label` came out as
		// `state.label.textContent = …`, writing through the var's cell as if
		// it were the element. The id is the one of the two that can move: a
		// var's name is read by everything the program wrote, while a handle
		// is reached through its binding.
		seen := ownerNames(o)
		for name := range shared {
			seen[name] = 1
		}
		rendered := map[*ir.Var]*ir.NodeInst{}
		var dup error
		// Every body the owner has, because an id is ambiguous wherever the
		// two nodes that share it are written.
		_ = ir.Walk(o.Stmts(), func(node ir.Node) error {
			n, ok := node.(*ir.NodeInst)
			if !ok || n.ID == "" {
				return nil
			}
			// The rename is keyed by *name*, because what it repairs is the
			// emitted namespace: html addresses a node by its id, and two
			// unrelated components each writing `#bar` collide there whether or
			// not they are related.
			k := seen[n.ID]
			seen[n.ID] = k + 1
			if k > 0 {
				renameNodeID(n, n.ID+"__"+strconv.Itoa(k), reads)
			}
			// The refusal is keyed by *symbol*, which is a narrower question
			// and a different one. A read is an ident bound to one var, and
			// declareNodeIDs runs per body: two unrelated `#bar`s are two vars
			// and each read says which it meant, while two spliced copies of
			// one body share theirs and neither read can. Only the second is
			// ambiguous -- keyed by name instead, an innocent `quiet()`
			// rendered twice was refused for a `#bar` that `reader()` read.
			if n.Handle == nil {
				return nil
			}
			if first, ok := rendered[n.Handle]; ok {
				if r := read[n.Handle]; r != nil && dup == nil {
					dup = fmt.Errorf("%s: `#%s` is read here, and %s renders more than one of it -- the read cannot say which; give each copy its own id, or pass the value it is read for as a prop", identPos(r), n.Handle.Name, nodePos(first))
				}
				return nil
			}
			rendered[n.Handle] = n
			return nil
		})
		if dup != nil {
			return dup
		}
	}
	return nil
}

// ownerNames is the names an owner's own declarations already hold in the
// emitted namespace, as the seen-count a node id is measured against: at one
// each, so the first id to want one is already a duplicate and moves.
// packageOwner is the owner that is neither a component nor a window, which is
// the package itself. ir.Owners always yields one.
func packageOwner(owners []ir.Owner) ir.Owner {
	for _, o := range owners {
		if o.Comp == nil && o.Win == nil {
			return o
		}
	}
	return ir.Owner{}
}

func ownerNames(o ir.Owner) map[string]int {
	out := map[string]int{}
	for _, v := range o.Vars {
		out[v.Name] = 1
	}
	for _, v := range o.Consts {
		out[v.Name] = 1
	}
	for _, f := range o.Funcs {
		out[f.Name] = 1
	}
	return out
}

// renameNodeID moves a node id and everything that spells it: the handle the
// checker bound, and every read of that handle. An emitter spells a read from
// the ident's own Name, so renaming the node alone left `tag__1` in the tree
// and `tag` in the code that reads it -- one name for the element and another
// for the reference to it, on every target.
func renameNodeID(n *ir.NodeInst, name string, reads map[*ir.Var][]*ir.Ident) {
	n.ID = name
	if n.Handle == nil {
		return
	}
	for _, id := range reads[n.Handle] {
		id.Name = name
	}
	n.Handle.Name = name
}

// handleIdents is every read of every `#id` handle, keyed by the binding. The
// singular handleReads below keeps one read per handle for a diagnostic to
// point at; this keeps them all, because a rename has to reach each one.
func handleIdents(stmts []ir.Stmt) map[*ir.Var][]*ir.Ident {
	out := map[*ir.Var][]*ir.Ident{}
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		id, ok := e.(*ir.Ident)
		if !ok {
			return nil
		}
		if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
			out[v] = append(out[v], id)
		}
		return nil
	})
	return out
}

// handleReads is every `#id` handle the statements read back by name, keyed by
// the binding rather than by the name, and valued at one of the reads so the
// diagnostic can point at source the user recognises.
func handleReads(stmts []ir.Stmt) map[*ir.Var]*ir.Ident {
	out := map[*ir.Var]*ir.Ident{}
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		id, ok := e.(*ir.Ident)
		if !ok {
			return nil
		}
		if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle && out[v] == nil {
			out[v] = id
		}
		return nil
	})
	return out
}

// identPos is where an ident was written, or "<unknown>" for one a pass
// synthesized.
func identPos(id *ir.Ident) string {
	if id == nil || id.AST == nil {
		return "<unknown>"
	}
	if p := id.AST.ExprPos(); p != nil && p.IsValid() {
		return p.String()
	}
	return "<unknown>"
}

type inlineCompState struct {
	pkg  *ir.Package
	main *ir.Component
	// local is what the package being lowered declares, as opposed to what it
	// renders. Nil falls back to reading pkg.Components.
	local map[*ir.Component]bool
	// onList is pkg.Components as the pass found it: what the prune at the end
	// is allowed to keep.
	onList map[*ir.Component]bool
	// hoist is where an inlined callee's own declarations land. Set per
	// container as run() walks.
	hoist hoistTarget
	// captureOnly, while spliceNestedCaptures runs, is the set of nested
	// components that round may splice. Nil for the main walk.
	captureOnly map[*ir.Component]bool
	cycles      map[*ir.Component]bool
	keep        map[*ir.Component]bool
	reactive    map[*ir.Var]bool
	platform    string
	// instSeq is passInlinePure's counter as well; see Options.instSeq.
	instSeq *int
	// demoted is the clones dropReceiver made receiverless, so the call sites
	// renameIdents repointed at them can give their receiver up too.
	demoted map[*ir.Func]bool
}

// Pointers rather than values because the append must be visible to the owner.
type hoistTarget struct {
	vars  *[]*ir.Var
	funcs *[]*ir.Func
	// method says a func hoisted here is still one: a component-body func is a
	// method and a package-level func is not, which is the pair of spellings
	// every backend already distinguishes. A callee's helper spliced into a
	// window or into the package body has to give up its receiver with the
	// declaration it was a method of, which this pass has just dropped --
	// left standing, route mode skipped it for having one and emitted
	// `s.Keep__inst0(…)` against a file that declared nothing of the name.
	method bool
}

func componentHoist(c *ir.Component) hoistTarget {
	return hoistTarget{vars: &c.Vars, funcs: &c.Funcs, method: true}
}

func windowHoist(pkg *ir.Package) hoistTarget {
	// A window's container, which is the package: a window is a rendering
	// root and owns nothing, so what an inlined callee declares inside one
	// belongs where the window's own declarations went.
	return hoistTarget{vars: &pkg.Vars, funcs: &pkg.Funcs}
}

// dropReceiver makes fn an ordinary func rather than a method of a component
// that no longer exists. The body needs no rewriting: it reaches the state it
// reads through the renamed clones above and never through `this`.
//
// ir.Param.Receiver is the flag rather than the name, as the checker sets it
// on exactly the synthetic parameter it prepended.
func dropReceiver(fn *ir.Func) {
	fn.Receiver = ""
	fn.RecvParam = nil
	fn.Params = slices.DeleteFunc(slices.Clone(fn.Params), func(p *ir.Param) bool {
		return p != nil && p.Receiver
	})
}

func (st *inlineCompState) run() error {
	st.keep = map[*ir.Component]bool{}
	if st.main != nil {
		st.keep[st.main] = true
	}
	for c := range st.cycles {
		st.keep[c] = true
	}
	if err := st.spliceNestedCaptures(); err != nil {
		return err
	}
	for {
		ch, anyFuncCh := false, false
		if st.main != nil {
			st.hoist = componentHoist(st.main)
			body, mch, err := st.inlineStmts(st.main.Body)
			if err != nil {
				return err
			}
			st.main.Body = body
			ch = mch
			// Indexed: inlining a call inside a func body appends to this very
			// slice, and the appended clones need walking too.
			for i := 0; i < len(st.main.Funcs); i++ {
				fbody, fch, err := st.inlineStmts(st.main.Funcs[i].Block)
				if err != nil {
					return err
				}
				st.main.Funcs[i].Block = fbody
				anyFuncCh = anyFuncCh || fch
			}
		}
		// The package's own body, which is where a root-family component is
		// instantiated: `main()` at the top of a file is a NodeInst here and
		// nowhere else, and its windows reach a backend only once it has been
		// spliced in.
		st.hoist = windowHoist(st.pkg)
		pbody, pch, err := st.inlineStmts(st.pkg.Body)
		if err != nil {
			return err
		}
		st.pkg.Body = pbody
		// Walk pkg.Windows: the visual tree for window-declaring apps lives
		// in Window.Body / Window.Funcs, not in main.Body. Components
		// instantiated inside windows must also be inlined.
		anyWinCh := false
		for _, w := range st.pkg.Windows {
			st.hoist = windowHoist(st.pkg)
			wbody, wch, err := st.inlineStmts(w.Children)
			if err != nil {
				return err
			}
			w.Children = wbody
			anyWinCh = anyWinCh || wch
		}
		if !ch && !anyFuncCh && !anyWinCh && !pch {
			break
		}
	}
	return nil
}

// rootComponent returns the component a harness has made the program's entry
// point, or nil -- which is every ordinary build, where a window is the root.
//
// A test build names the component under test, because that is what the
// harness renders: left to itself, the inliner would flatten it into the
// program's own root and rename its state per instance. `main` used to be
// that root by convention, and CodegenCtx.RootDecl is the same answer for
// codegen.
func rootComponent(pkg *ir.Package, opts Options) *ir.Component {
	if opts.RootComponent == "" {
		return nil
	}
	for _, c := range pkg.Components {
		if c.Name == opts.RootComponent {
			return c
		}
	}
	return nil
}

// findRecursiveCycles returns the set of components participating in any
// call cycle (including self-recursion). Edges follow NodeInst.Component.
func findRecursiveCycles(pkg *ir.Package, opts Options) map[*ir.Component]bool {
	edges := map[*ir.Component]map[*ir.Component]bool{}
	for _, c := range pkg.Components {
		edges[c] = map[*ir.Component]bool{}
		collectCalleeEdges(c, edges[c])
	}
	// A window is a root and not a node in the component graph, so its edges
	// belong to no component: a cycle among components is found from the
	// components alone. They used to be attributed to `main`, which was the
	// root by convention -- and `window { main }` then read as main calling
	// itself, so every such program elected a runtime instance instead of
	// inlining the component into the window.
	//
	// A harness that made a component the root is the exception, and there the
	// windows are gone.
	if root := rootComponent(pkg, opts); root != nil {
		for _, w := range pkg.Windows {
			collectCalleeEdges(w, edges[root])
		}
	}
	return tarjanCycles(edges)
}

// collectCalleeEdges records every component root instantiates, wherever in
// what root owns the instantiation is written.
//
// ir.Walk suits this exactly: it stops at NodeInst.Component, which is the
// edge being collected rather than a body to follow.
func collectCalleeEdges(root any, out map[*ir.Component]bool) {
	_ = ir.Walk(root, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && inst.Component != nil {
			out[inst.Component] = true
		}
		return nil
	})
}

// tarjanCycles runs Tarjan's SCC algorithm and returns the set of nodes
// in any non-trivial SCC (size > 1, or size 1 with a self-edge).
func tarjanCycles(edges map[*ir.Component]map[*ir.Component]bool) map[*ir.Component]bool {
	cycles := map[*ir.Component]bool{}
	idx := 0
	indices := map[*ir.Component]int{}
	lowlinks := map[*ir.Component]int{}
	onStack := map[*ir.Component]bool{}
	var stack []*ir.Component

	var strongconnect func(v *ir.Component)
	strongconnect = func(v *ir.Component) {
		indices[v] = idx
		lowlinks[v] = idx
		idx++
		stack = append(stack, v)
		onStack[v] = true
		for w := range edges[v] {
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}
		if lowlinks[v] == indices[v] {
			var scc []*ir.Component
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				for _, n := range scc {
					cycles[n] = true
				}
			} else if edges[scc[0]][scc[0]] {
				cycles[scc[0]] = true
			}
		}
	}
	for v := range edges {
		if _, seen := indices[v]; !seen {
			strongconnect(v)
		}
	}
	return cycles
}

// retainComponents returns a new slice containing only components in keep,
// preserving relative order.
func retainComponents(in []*ir.Component, keep map[*ir.Component]bool) []*ir.Component {
	out := make([]*ir.Component, 0, len(in))
	for _, c := range in {
		if keep[c] {
			out = append(out, c)
		}
	}
	return out
}

// --- inliner helpers ---

func (st *inlineCompState) freshSuffix() string { return freshInstSuffix(st.instSeq) }

// freshInstSuffix hands out the next `__instN`.
func freshInstSuffix(seq *int) string {
	n := *seq
	*seq++
	return "__inst" + strconv.Itoa(n)
}

// renameIdents rewrites Ident.Name via renames and repoints Ident.Sym via
// symRenames. Repointing Sym is essential: after inlining, the cloned
// vars/funcs live in main, and downstream passes (notably reactivity) collect
// those clones and match dependencies by Sym pointer. Leaving Sym at the
// original decl makes a reactive `if` referencing an inlined var resolve to a
// var no longer in scope, so the dependency is missed and the branch loses
// reactivity. Mutates exprs in place; caller passes a deep clone.
func renameIdents(stmts []ir.Stmt, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) []ir.Stmt {
	if len(renames) == 0 && len(symRenames) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		// A call names its callee on Call.Func, not through an Ident, so
		// repointing idents alone left `bump()` inside an inlined body calling
		// the original declaration -- whose body still reads the component's
		// own vars, which after inlining exist only under the instance's
		// names. The clone that was correct went uncalled and the caller got
		// the one that was not. Returned unchanged so the walk still descends
		// into the arguments.
		if call, ok := e.(*ir.Call); ok && call.Func != nil {
			if newSym, ok2 := symRenames[call.Func]; ok2 {
				if fn, ok3 := newSym.(*ir.Func); ok3 {
					call.Func = fn
				}
			}
			return e
		}
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return e
		}
		old := id.Sym
		if newName, ok2 := renames[old]; ok2 {
			id.Name = newName
		}
		if newSym, ok2 := symRenames[old]; ok2 {
			id.Sym = newSym
		}
		return e
	})
	return w.stmts(stmts)
}

func renameInExpr(e ir.Expr, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = renameIdents(tmp, renames, symRenames)
	return tmp[0].(*ir.LocalVar).Init
}

func substituteParamsExpr(e ir.Expr, bindings map[string]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = substituteParams(tmp, bindings)
	return tmp[0].(*ir.LocalVar).Init
}

func (st *inlineCompState) inlinable(comp *ir.Component) bool {
	if comp == nil || (st.main != nil && comp == st.main) {
		return false
	}
	if st.captureOnly != nil && !st.captureOnly[comp] {
		return false
	}
	if st.cycles[comp] {
		return false
	}
	// Platform primitives and stdlib wrappers with no body cannot be inlined.
	if len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0 {
		return false
	}
	// Only inline components declared in this package, or a stdlib component
	// this build's platform extension specialized: passPlatformExtensionBody
	// swapped that body and its vars into the component, and state declared
	// there is per-instance for exactly the reasons a user component's var
	// is. A pure override never reaches here — passInlinePure substituted it
	// already — so this is the impure override's path to the same renames.
	if !st.isLocalComponent(comp) && !st.specializedHere(comp) {
		return false
	}
	return true
}

// rendersNothing reports whether comp puts nothing in the rendered tree.
//
// Conservative: anything it cannot account for renders something, so a body
// shape not thought of costs an instance election rather than a wrong inline.
// It does not ask what a nested node renders, because a stdlib component is
// still abstract here -- a descent reads `text` as rendering nothing.
func (st *inlineCompState) rendersNothing(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	renders := false
	_ = ir.Walk(comp.Body, func(node ir.Node) error {
		if renders {
			return nil
		}
		switch n := node.(type) {
		case *ir.NodeInst:
			if !isEffectNode(n) {
				renders = true
			}
		case *ir.SlotInst, *ir.ErrorBoundary, *ir.CanvasRedrawStmt:
			renders = true
		}
		return nil
	})
	return !renders
}

// specializedHere reports whether comp carries a platform extension body for
// the platform being lowered for. With no platform (LSP, format) nothing is
// specialized and nothing qualifies.
func (st *inlineCompState) specializedHere(comp *ir.Component) bool {
	if st.platform == "" || comp.PlatformOverrides == nil {
		return false
	}
	_, ok := comp.PlatformOverrides[st.platform]
	return ok
}

// isLocalComponent reports whether comp is declared in the package being
// lowered. Read from the set Lower captured before it widened pkg.Components
// to every component this build renders: an imported component is lowered like
// any other now, but it is still not this package's to inline away.
func (st *inlineCompState) isLocalComponent(comp *ir.Component) bool {
	if st.local != nil {
		return st.local[comp]
	}
	return slices.Contains(st.pkg.Components, comp)
}

func (st *inlineCompState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, bool, error) {
	return st.inlineStmtsCtx(stmts, reactiveCtx{})
}

// reactiveCtx is what the statements below a control-flow construct are written
// inside: `in` is whether a node here needs reconciling, `repeated` whether the
// position holds one copy of the body or many. A `for` sets `repeated` even when
// it is not reactive -- what makes one hoisted var wrong is the copies.
type reactiveCtx struct {
	in       bool
	repeated bool
	// loopReactive is whether the nearest enclosing `for` yields a copy that
	// gets state of its own -- either because it iterates something the program
	// can change, or because the loop itself sits somewhere reactive and is
	// rebuilt as a unit.
	//
	// It is not `in`, and the two part company in both directions: a const loop
	// holding a reactive `if` sets `in` and still hands every pass the same
	// hoisted vars, while a const loop *inside* a reactive `if` also sets `in`
	// and does not. What separates them is where `in` was true -- outside the
	// loop, or under it.
	loopReactive bool
	// loopPos is where the nearest enclosing `for` was written. The node's own
	// position is not a substitute: a lifetime reached through a component sits
	// in *that declaration*, so a diagnostic about the loop would otherwise cite
	// a line with no loop on it.
	loopPos string
}

func (st *inlineCompState) inlineStmtsCtx(stmts []ir.Stmt, rc reactiveCtx) ([]ir.Stmt, bool, error) {
	changed := false
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		repl, ch, err := st.inlineStmtCtx(s, rc)
		if err != nil {
			return nil, false, err
		}
		changed = changed || ch
		out = append(out, repl...)
	}
	return out, changed, nil
}

func (st *inlineCompState) inlineStmtCtx(s ir.Stmt, rc reactiveCtx) ([]ir.Stmt, bool, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, rc)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		// A window is a rendering root and instantiates nothing this pass may
		// splice: it stays where it was written, with whatever its body held
		// now inlined. Everything below asks what to do with a *component*
		// instantiation, and a window is not one.
		if ir.IsWindowNode(n) {
			return []ir.Stmt{n}, chCh, nil
		}
		anyHandlerCh := false
		for _, h := range n.Handlers {
			if h.Func == nil {
				continue
			}
			hbody, hCh, err := st.inlineStmtsCtx(h.Func.Block, reactiveCtx{})
			if err != nil {
				return nil, false, err
			}
			h.Func.Block = hbody
			anyHandlerCh = anyHandlerCh || hCh
		}
		// Skip inlining when this NodeInst sits inside a reactive control-flow
		// construct, or when its target is part of a recursive cycle. Either
		// case will be lowered to a CreateComponent intrinsic call by later
		// passes (passReactivity / passDeclarative).
		//
		// Renders nothing is the exception: no node here to reconcile, and
		// passEffect already reads the enclosing `if` as the bracket's
		// position. Not under a `for` -- the body is spliced once and its state
		// hoisted once, so the copies would share one var.
		if err := refuseRepeatedLifetime(n, rc); err != nil {
			return nil, false, err
		}
		if rc.in && !rc.repeated && st.inlinable(n.Component) && st.rendersNothing(n.Component) {
			spliced, err := st.expandCall(n)
			if err != nil {
				return nil, false, err
			}
			return spliced, true, nil
		}
		if st.captureOnly != nil && rc.in && st.captureOnly[n.Component] {
			return nil, false, fmt.Errorf("%s: %q reads the state of the body it is declared in and is instantiated inside a reactive if or for, where each copy needs state of its own -- capture would hand every copy the one cell the owner holds; lift it to a top-level component and pass what it reads as props", nodePos(n), n.Component.Name)
		}
		// Left to the main walk: a capturing body cannot be the runtime
		// instance this branch elects, and the pre-pass already refused it.
		if st.captureOnly == nil && n.Component != nil && (rc.in || st.cycles[n.Component]) {
			st.keep[n.Component] = true
			// The one place that knows: this instantiation is built while the
			// program runs, so the declaration needs a runtime of its own.
			n.Component.RuntimeInstance = true
			return []ir.Stmt{n}, chCh || anyHandlerCh, nil
		}
		if !st.inlinable(n.Component) {
			// The node survives, so the declaration behind it does too: the
			// backend will read that body, and the passes after this one have
			// to have lowered it. Dropping it from pkg.Components here is what
			// left an imported component's canvas un-lowered. Only what the
			// list already carries -- a primitive was never on it, and putting
			// one there would have the package declaring `text` and `button`.
			if st.captureOnly == nil && n.Component != nil && st.onList[n.Component] {
				st.keep[n.Component] = true
			}
			return []ir.Stmt{n}, chCh || anyHandlerCh, nil
		}
		spliced, err := st.expandCall(n)
		if err != nil {
			return nil, false, err
		}
		return spliced, true, nil
	case *ir.If:
		inner := reactiveCtx{in: rc.in || dependsOnReactiveVar(n.Cond, st.reactive), repeated: rc.repeated, loopReactive: rc.loopReactive}
		body, ch1, err := st.inlineStmtsCtx(n.Body, inner)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmtsCtx(n.Else, inner)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.For:
		reactiveIter := dependsOnReactiveVar(n.Iter, st.reactive)
		inner := reactiveCtx{
			in:       rc.in || reactiveIter,
			repeated: true,
			// `rc.in` as it was *entering* the loop, not inside it. A loop that
			// already sits in a reactive position re-renders as a unit, so its
			// copies are built at run time and each gets a record of its own --
			// which is the same thing a reactive iterable buys. A reactive `if`
			// written *inside* a const loop is the opposite case and reaches
			// the node with the same `in`, which is why this is read here and
			// not there.
			loopReactive: rc.in || rc.loopReactive || reactiveIter,
			loopPos:      forPos(n),
		}
		body, ch1, err := st.inlineStmtsCtx(n.Body, inner)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmtsCtx(n.Else, inner)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.SlotInst:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, rc)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		return []ir.Stmt{n}, chCh, nil
	case *ir.ErrorBoundary:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, rc)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		hCh := false
		if n.Handler != nil && n.Handler.Func != nil {
			body, b, err := st.inlineStmtsCtx(n.Handler.Func.Block, reactiveCtx{})
			if err != nil {
				return nil, false, err
			}
			n.Handler.Func.Block = body
			hCh = b
		}
		return []ir.Stmt{n}, chCh || hCh, nil
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle, *ir.ContextProvider,
		*ir.Break, *ir.Continue:
		// Leaf/imperative stmts — no NodeInsts to inline.
		return []ir.Stmt{s}, false, nil
	default:
		panic(fmt.Sprintf("inlineCompState.inlineStmt: unhandled %T", n))
	}
}

// walkExprIdents calls visit on every *ir.Ident reachable from e.
func walkExprIdents(e ir.Expr, visit func(*ir.Ident)) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Ident:
		visit(n)
	case *ir.Select:
		walkExprIdents(n.Operand, visit)
	case *ir.Index:
		walkExprIdents(n.Operand, visit)
		walkExprIdents(n.Idx, visit)
	case *ir.Binary:
		walkExprIdents(n.Left, visit)
		walkExprIdents(n.Right, visit)
	case *ir.Unary:
		walkExprIdents(n.Operand, visit)
	case *ir.Ternary:
		walkExprIdents(n.Cond, visit)
		walkExprIdents(n.Then, visit)
		walkExprIdents(n.Else, visit)
	case *ir.Conversion:
		walkExprIdents(n.Operand, visit)
	case *ir.Call:
		if n.Receiver != nil {
			walkExprIdents(n.Receiver, visit)
		}
		for _, a := range n.Args {
			walkExprIdents(a.Value, visit)
		}
	case *ir.ListLit:
		for _, el := range n.Elems {
			walkExprIdents(el, visit)
		}
	case *ir.StructLit:
		for _, f := range n.Fields {
			walkExprIdents(f.Value, visit)
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			walkExprIdents(en.Key, visit)
			walkExprIdents(en.Value, visit)
		}
	case *ir.Spread:
		walkExprIdents(n.Operand, visit)
	}
}

// dependsOnReactiveVar reports whether an expression reads any var
// from the reactive set. Used to identify *ir.If/*ir.For whose body
// must be left as a runtime construct rather than statically inlined.
func dependsOnReactiveVar(e ir.Expr, reactive map[*ir.Var]bool) bool {
	if e == nil || len(reactive) == 0 {
		return false
	}
	found := false
	walkExprIdents(e, func(id *ir.Ident) {
		if v, ok := id.Sym.(*ir.Var); ok && reactive[v] {
			found = true
		}
	})
	return found
}

func (st *inlineCompState) expandCall(n *ir.NodeInst) ([]ir.Stmt, error) {
	comp := n.Component
	suffix := st.freshSuffix()

	renames := map[ir.Symbol]string{}
	symRenames := map[ir.Symbol]ir.Symbol{}

	hoist := st.hoist
	varStart := len(*hoist.vars)
	for _, v := range comp.Vars {
		clone := cloneVarShallow(v)
		clone.Name = v.Name + suffix
		clone.Init = deepCloneExpr(v.Init)
		// Synthesized context Vars (added by passNoContext) can be
		// overridden at the call site by a hidden __ctx_<name> arg in
		// n.Props. When present, that arg supersedes ctx.Default.
		if v.Synthesized {
			for _, arg := range n.Props {
				if arg.Name == v.Name {
					clone.Init = deepCloneExpr(arg.Value)
					break
				}
			}
		}
		renames[v] = clone.Name
		symRenames[v] = clone
		// The clone is as reactive as the original. st.reactive was computed
		// once, before this pass created any of these, so a `for` iterating an
		// inlined component's own state read as non-reactive -- and the
		// instantiation inside it was inlined too, giving every element of the
		// loop one shared cell for what the component declared per instance.
		if st.reactive[v] {
			st.reactive[clone] = true
		}
		// And it holds what the original held: the points-to result is keyed
		// by declaration, so a clone is a slot nothing has analysed.
		carryPointsTo(st.pkg, ir.SlotVarKey(v), ir.SlotVarKey(clone))
		*hoist.vars = append(*hoist.vars, clone)
	}
	funcStart := len(*hoist.funcs)
	for _, f := range comp.Funcs {
		clone := cloneFuncShallow(f)
		clone.Name = f.Name + suffix
		clone.Block = deepCloneStmts(f.Block)
		renames[f] = clone.Name
		symRenames[f] = clone
		if !hoist.method {
			dropReceiver(clone)
			st.demoted[clone] = true
		}
		carryPointsTo(st.pkg, ir.SlotReturnKey(f), ir.SlotReturnKey(clone))
		*hoist.funcs = append(*hoist.funcs, clone)
	}

	// Apply renames to every hoisted block.
	for i := varStart; i < len(*hoist.vars); i++ {
		if (*hoist.vars)[i].Init != nil {
			(*hoist.vars)[i].Init = renameInExpr((*hoist.vars)[i].Init, renames, symRenames)
		}
		// A var handler's body reads and writes the instance's state like any
		// other block the callee wrote. Left unrenamed it kept pointing at the
		// pre-inlining name, which every instance shares -- a silent
		// wrong-variable write, since the emitters splice it verbatim.
		for _, h := range (*hoist.vars)[i].Handlers {
			if h.Func != nil {
				h.Func.Block = renameIdents(h.Func.Block, renames, symRenames)
			}
		}
	}
	for i := funcStart; i < len(*hoist.funcs); i++ {
		(*hoist.funcs)[i].Block = renameIdents((*hoist.funcs)[i].Block, renames, symRenames)
	}
	body := deepCloneStmts(comp.Body)
	body = renameIdents(body, renames, symRenames)

	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		var val ir.Expr
		for _, arg := range n.Props {
			if arg.Name == p.Name {
				val = arg.Value
				break
			}
		}
		if val == nil {
			val = p.Default
		}
		if val == nil {
			// A stdlib prop with no declared default renders as its type's
			// zero value, which is what leaving one out means. Binding nothing
			// left the parameter's name standing in the inlined body, which
			// only ever mattered for a body that *reads* a prop the call site
			// omitted -- a platform override does, so `style` on a
			// `rect(x, y, w, h)` emitted an identifier nothing declares.
			val = ir.DeclaredDefault(p.Type)
		}
		if val != nil {
			bindings[p.Name] = val
		}
	}
	body = substituteParams(body, bindings)

	// Prop refs may appear in the hoisted callee-scope Vars/Funcs too.
	for i := varStart; i < len(*hoist.vars); i++ {
		(*hoist.vars)[i].Init = substituteParamsExpr((*hoist.vars)[i].Init, bindings)
		for _, h := range (*hoist.vars)[i].Handlers {
			if h.Func != nil {
				h.Func.Block = substituteParams(h.Func.Block, bindings)
			}
		}
	}
	for i := funcStart; i < len(*hoist.funcs); i++ {
		(*hoist.funcs)[i].Block = substituteParams((*hoist.funcs)[i].Block, bindings)
	}
	body = substituteSlots(body, n)
	body = substituteEvents(body, n.Handlers)

	return body, nil
}

// carryPointsTo copies one slot's candidates and colour onto a second key, for
// a declaration a pass has cloned. A key with nothing recorded is left absent,
// so "no candidates" and "not a funcvar slot" stay distinguishable.
func carryPointsTo(pkg *ir.Package, from, to ir.PointsToKey) {
	if pkg == nil || pkg.PointsTo == nil {
		return
	}
	if cands, ok := pkg.PointsTo.Sites[from]; ok {
		pkg.PointsTo.Sites[to] = append([]*ir.Func{}, cands...)
	}
	if col, ok := pkg.PointsTo.SlotColor[from]; ok {
		pkg.PointsTo.SlotColor[to] = col
	}
}

func cloneVarShallow(v *ir.Var) *ir.Var {
	c := *v
	c.Handlers = nil
	for _, h := range v.Handlers {
		hc := *h
		if h.Func != nil {
			fc := *h.Func
			fc.Block = deepCloneStmts(h.Func.Block)
			hc.Func = &fc
		}
		c.Handlers = append(c.Handlers, &hc)
	}
	return &c
}

func cloneFuncShallow(f *ir.Func) *ir.Func {
	c := *f
	c.Block = nil
	return &c
}

// sharesLifetimeState reports whether splicing comp twice would give two
// lifetimes one set of cells to release through.
//
// **Holding an `effect` is not enough**, and asking only that refused working
// programs. `passEffect` keys a bracket's own bookkeeping by list --
// `__effectN_live` and `__effectN_desired` hold an entry per key -- so N copies
// of a bare lifetime mount and unmount independently and correctly. What has no
// list is a *component's* state: `passHoistState` gives each declared var one
// cell on the owner, and a non-reactive loop splices the body once, so every
// pass writes the same cell. A handle stored there is overwritten by the second
// mount and the first schedule can no longer be reached to be stopped.
//
// So the question is the conjunction: a var this component declares, which an
// effect's own handlers touch. A lifetime that closes over nothing of its
// component's is as safe here as a bare one, and a component whose state no
// bracket reads is the ordinary shared-cell cost the loop already carries.
//
// Runs before passEffect, so a bracket is still the node a program wrote.
func sharesLifetimeState(comp *ir.Component) bool {
	if comp == nil || len(comp.Vars) == 0 {
		return false
	}
	own := make(map[*ir.Var]bool, len(comp.Vars))
	for _, v := range comp.Vars {
		own[v] = true
	}
	found := false
	_ = ir.Walk(comp.Body, func(nd ir.Node) error {
		n, ok := nd.(*ir.NodeInst)
		if !ok || !isEffectNode(n) || found {
			return nil
		}
		for _, h := range n.Handlers {
			if h.Func == nil {
				continue
			}
			_ = ir.Walk(h.Func.Block, func(in ir.Node) error {
				if id, ok := in.(*ir.Ident); ok {
					if v, ok := id.Sym.(*ir.Var); ok && own[v] {
						found = true
					}
				}
				return nil
			})
		}
		return nil
	})
	return found
}

// forPos is where a loop was written, or "" when the IR carries no position.
func forPos(n *ir.For) string {
	if p := ir.StmtPos(n); p.IsValid() {
		return p.String()
	}
	return ""
}

// refuseRepeatedLifetime stops a component that brackets a lifetime from being
// spliced into a position that holds many copies of it.
//
// A `for` sets `repeated` whether or not it is reactive, and a *reactive* one
// elects a RuntimeInstance below, so each copy gets state of its own. A
// non-reactive one -- a `const` iterable, where nothing can change -- does not:
// the body is spliced once and its vars hoisted once, so every copy shares the
// one handle. For an ordinary component that is the documented cost of sharing
// a cell; for a lifetime it is a resource nothing can release, because the
// second mount overwrites the handle the first would have been stopped through.
//
// Refused rather than tolerated because the failure is silent and unbounded: a
// timer under such a loop leaves a goroutine running for the life of the
// process. #245 is the real fix -- route a stateful component to a runtime
// instance whether or not the position is reactive -- and until it lands this
// is the loud half of what `main` did by accident.
func refuseRepeatedLifetime(n *ir.NodeInst, rc reactiveCtx) error {
	if !rc.repeated || rc.loopReactive || !sharesLifetimeState(n.Component) {
		return nil
	}
	// Two positions, because they are usually two different lines: the loop is
	// what the rule is about, and the node is where the lifetime entered it --
	// which for a wrapped one is inside a declaration written somewhere else.
	where := ""
	if rc.loopPos != "" && rc.loopPos != nodePos(n) {
		where = fmt.Sprintf(" (reached from %s)", nodePos(n))
	}
	at := rc.loopPos
	if at == "" {
		at = nodePos(n)
	}
	return fmt.Errorf("%s: this loop is not reactive and %q%s brackets a lifetime, so every pass would share one set of its state and only the last could be released -- a schedule opened by the others is never closed. Iterate something the program can change (a `var`, not a `const`), which gives each pass state of its own. This is a lowering rule, so a target that unrolls the loop instead -- html on --lang none -- builds the same source; see #245",
		at, n.Component.Name, where)
}

// clearDemotedReceivers drops the receiver at every call site that names a func
// dropReceiver made receiverless.
//
// Once the whole pass has run, rather than as each call is spliced: a call
// reaches its callee through ir.Call.Func, which renameIdents repoints, so the
// site that needs fixing may be in a body inlined after the declaration was.
func clearDemotedReceivers(pkg *ir.Package, demoted map[*ir.Func]bool) {
	if len(demoted) == 0 {
		return
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if call, ok := n.(*ir.Call); ok && demoted[call.Func] {
			call.Receiver = nil
		}
		return nil
	})
}
