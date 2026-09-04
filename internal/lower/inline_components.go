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
	// main may be nil: a program declaring its windows at top level has none,
	// and its visual tree lives in pkg.Windows instead.
	main := rootComponent(pkg, opts)
	if main == nil && len(pkg.Windows) == 0 {
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
	st := &inlineCompState{pkg: pkg, main: main, cycles: cycles, reactive: reactive, platform: opts.Platform, local: opts.localComponents, onList: onList}
	if err := st.run(); err != nil {
		return err
	}
	pkg.Components = retainComponents(pkg.Components, st.keep)
	uniqueNodeIDs(pkg)
	return nil
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
func uniqueNodeIDs(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	for _, o := range ir.Owners(pkg) {
		seen := map[string]int{}
		// Every body the owner has, because an id is ambiguous wherever the
		// two nodes that share it are written.
		_ = ir.Walk(o.Stmts, func(node ir.Node) error {
			n, ok := node.(*ir.NodeInst)
			if !ok || n.ID == "" {
				return nil
			}
			k := seen[n.ID]
			seen[n.ID] = k + 1
			if k > 0 {
				n.ID = n.ID + "__" + strconv.Itoa(k)
			}
			return nil
		})
	}
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
	// container as run() walks: a component holds all three slices, a window
	// holds vars and funcs and borrows pkg.Timers.
	hoist       hoistTarget
	cycles      map[*ir.Component]bool
	keep        map[*ir.Component]bool
	reactive    map[*ir.Var]bool
	platform    string
	instCounter int
}

// Pointers rather than values because the append must be visible to the owner.
type hoistTarget struct {
	vars   *[]*ir.Var
	funcs  *[]*ir.Func
	timers *[]*ir.Timer
}

func componentHoist(c *ir.Component) hoistTarget {
	return hoistTarget{vars: &c.Vars, funcs: &c.Funcs, timers: &c.Timers}
}

// A window has no timers of its own: the checker files a timer declared
// outside a component on the package, so that is where a hoisted one goes too.
func windowHoist(w *ir.Window, pkg *ir.Package) hoistTarget {
	return hoistTarget{vars: &w.Vars, funcs: &w.Funcs, timers: &pkg.Timers}
}

func (st *inlineCompState) run() error {
	st.keep = map[*ir.Component]bool{}
	if st.main != nil {
		st.keep[st.main] = true
	}
	for c := range st.cycles {
		st.keep[c] = true
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
		// Walk pkg.Windows: the visual tree for window-declaring apps lives
		// in Window.Body / Window.Funcs, not in main.Body. Components
		// instantiated inside windows must also be inlined.
		anyWinCh := false
		for _, w := range st.pkg.Windows {
			st.hoist = windowHoist(w, st.pkg)
			wbody, wch, err := st.inlineStmts(w.Body)
			if err != nil {
				return err
			}
			w.Body = wbody
			anyWinCh = anyWinCh || wch
			for i := 0; i < len(w.Funcs); i++ {
				fbody, fch, err := st.inlineStmts(w.Funcs[i].Block)
				if err != nil {
					return err
				}
				w.Funcs[i].Block = fbody
				anyWinCh = anyWinCh || fch
			}
		}
		if !ch && !anyFuncCh && !anyWinCh {
			break
		}
	}
	return nil
}

// rootComponent returns the component lowering treats as the program's entry
// point: the one opts names, and otherwise the one called "main".
//
// A test build names the component under test, because that is what the
// harness renders. Left to find "main", the inliner flattens the component
// under test into the program's own root and renames its state per instance.
func rootComponent(pkg *ir.Package, opts Options) *ir.Component {
	want := opts.RootComponent
	if want == "" {
		want = "main"
	}
	for _, c := range pkg.Components {
		if c.Name == want {
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
	// Also scan pkg.Windows so components instantiated inside window bodies
	// participate in cycle detection. Use a synthetic "main" edge set since
	// windows are not independent cycle roots — they live in main's scope.
	if main := rootComponent(pkg, opts); main != nil {
		for _, w := range pkg.Windows {
			collectCalleeEdges(w, edges[main])
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

func (st *inlineCompState) freshSuffix() string {
	n := st.instCounter
	st.instCounter++
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
	if st.cycles[comp] {
		return false
	}
	// Platform primitives and stdlib wrappers with no body cannot be inlined.
	if len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0 && len(comp.Timers) == 0 {
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
	return st.inlineStmtsCtx(stmts, false)
}

func (st *inlineCompState) inlineStmtsCtx(stmts []ir.Stmt, inReactive bool) ([]ir.Stmt, bool, error) {
	changed := false
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		repl, ch, err := st.inlineStmtCtx(s, inReactive)
		if err != nil {
			return nil, false, err
		}
		changed = changed || ch
		out = append(out, repl...)
	}
	return out, changed, nil
}

func (st *inlineCompState) inlineStmtCtx(s ir.Stmt, inReactive bool) ([]ir.Stmt, bool, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, inReactive)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		anyHandlerCh := false
		for _, h := range n.Handlers {
			if h.Func == nil {
				continue
			}
			hbody, hCh, err := st.inlineStmtsCtx(h.Func.Block, false)
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
		if n.Component != nil && (inReactive || st.cycles[n.Component]) {
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
			if n.Component != nil && st.onList[n.Component] {
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
		bodyReactive := inReactive || dependsOnReactiveVar(n.Cond, st.reactive)
		body, ch1, err := st.inlineStmtsCtx(n.Body, bodyReactive)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmtsCtx(n.Else, bodyReactive)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.For:
		bodyReactive := inReactive || dependsOnReactiveVar(n.Iter, st.reactive)
		body, ch1, err := st.inlineStmtsCtx(n.Body, bodyReactive)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmtsCtx(n.Else, bodyReactive)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.SlotInst:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, inReactive)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		return []ir.Stmt{n}, chCh, nil
	case *ir.ErrorBoundary:
		ch, chCh, err := st.inlineStmtsCtx(n.Children, inReactive)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		hCh := false
		if n.Handler != nil && n.Handler.Func != nil {
			body, b, err := st.inlineStmtsCtx(n.Handler.Func.Block, false)
			if err != nil {
				return nil, false, err
			}
			n.Handler.Func.Block = body
			hCh = b
		}
		return []ir.Stmt{n}, chCh || hCh, nil
	case *ir.Window:
		// Window stmts live in component bodies when `window { }` is declared
		// inside a component (rather than at document root). Recurse into the
		// window's body so component NodeInsts nested inside it are inlined.
		body, ch, err := st.inlineStmtsCtx(n.Body, inReactive)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		anyFuncCh := false
		for _, f := range n.Funcs {
			fbody, fch, err := st.inlineStmtsCtx(f.Block, false)
			if err != nil {
				return nil, false, err
			}
			f.Block = fbody
			anyFuncCh = anyFuncCh || fch
		}
		return []ir.Stmt{n}, ch || anyFuncCh, nil
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
		*hoist.vars = append(*hoist.vars, clone)
	}
	funcStart := len(*hoist.funcs)
	for _, f := range comp.Funcs {
		clone := cloneFuncShallow(f)
		clone.Name = f.Name + suffix
		clone.Block = deepCloneStmts(f.Block)
		renames[f] = clone.Name
		symRenames[f] = clone
		*hoist.funcs = append(*hoist.funcs, clone)
	}
	timerStart := len(*hoist.timers)
	for _, t := range comp.Timers {
		clone := *t
		clone.Interval = deepCloneExpr(t.Interval)
		clone.Enabled = deepCloneExpr(t.Enabled)
		if t.Handler != nil {
			h := *t.Handler
			h.Block = deepCloneStmts(t.Handler.Block)
			clone.Handler = &h
		}
		*hoist.timers = append(*hoist.timers, &clone)
	}

	// Apply renames to every hoisted block.
	for i := varStart; i < len(*hoist.vars); i++ {
		if (*hoist.vars)[i].Init != nil {
			(*hoist.vars)[i].Init = renameInExpr((*hoist.vars)[i].Init, renames, symRenames)
		}
	}
	for i := funcStart; i < len(*hoist.funcs); i++ {
		(*hoist.funcs)[i].Block = renameIdents((*hoist.funcs)[i].Block, renames, symRenames)
	}
	for i := timerStart; i < len(*hoist.timers); i++ {
		t := (*hoist.timers)[i]
		if t.Handler != nil {
			t.Handler.Block = renameIdents(t.Handler.Block, renames, symRenames)
		}
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
		if val != nil {
			bindings[p.Name] = val
		}
	}
	body = substituteParams(body, bindings)

	// Prop refs may appear in the hoisted callee-scope Vars/Funcs/Timers too.
	for i := varStart; i < len(*hoist.vars); i++ {
		(*hoist.vars)[i].Init = substituteParamsExpr((*hoist.vars)[i].Init, bindings)
	}
	for i := funcStart; i < len(*hoist.funcs); i++ {
		(*hoist.funcs)[i].Block = substituteParams((*hoist.funcs)[i].Block, bindings)
	}
	for i := timerStart; i < len(*hoist.timers); i++ {
		t := (*hoist.timers)[i]
		t.Interval = substituteParamsExpr(t.Interval, bindings)
		t.Enabled = substituteParamsExpr(t.Enabled, bindings)
		if t.Handler != nil {
			t.Handler.Block = substituteParams(t.Handler.Block, bindings)
		}
	}

	body = substituteSlots(body, n)
	body = substituteEvents(body, n.Handlers)

	return body, nil
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
