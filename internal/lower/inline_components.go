package lower

import (
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

func lowerInlineComponents(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	main := mainComponent(pkg)
	if main == nil {
		return nil
	}
	cycles := findRecursiveCycles(pkg)
	st := &inlineCompState{pkg: pkg, main: main, cycles: cycles}
	if err := st.run(); err != nil {
		return err
	}
	pkg.Components = retainComponents(pkg.Components, st.keep)
	return nil
}

type inlineCompState struct {
	pkg         *ir.Package
	main        *ir.Component
	cycles      map[*ir.Component]bool
	keep        map[*ir.Component]bool
	instCounter int
}

func (st *inlineCompState) run() error {
	st.keep = map[*ir.Component]bool{st.main: true}
	for c := range st.cycles {
		st.keep[c] = true
	}
	for {
		body, ch, err := st.inlineStmts(st.main.Body)
		if err != nil {
			return err
		}
		st.main.Body = body
		anyFuncCh := false
		for _, f := range st.main.Funcs {
			fbody, fch, err := st.inlineStmts(f.Block)
			if err != nil {
				return err
			}
			f.Block = fbody
			anyFuncCh = anyFuncCh || fch
		}
		if !ch && !anyFuncCh {
			break
		}
	}
	return nil
}

// mainComponent returns the package's main component, or nil if absent.
func mainComponent(pkg *ir.Package) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == "main" {
			return c
		}
	}
	return nil
}

// findRecursiveCycles returns the set of components participating in any
// call cycle (including self-recursion). Edges follow NodeInst.Component
// from each component's body, funcs, and nested control-flow.
func findRecursiveCycles(pkg *ir.Package) map[*ir.Component]bool {
	edges := map[*ir.Component]map[*ir.Component]bool{}
	for _, c := range pkg.Components {
		edges[c] = map[*ir.Component]bool{}
		collectCalleeEdges(c.Body, edges[c])
		for _, f := range c.Funcs {
			collectCalleeEdges(f.Block, edges[c])
		}
	}
	return tarjanCycles(edges)
}

func collectCalleeEdges(stmts []ir.Stmt, out map[*ir.Component]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Component != nil {
				out[n.Component] = true
			}
			collectCalleeEdges(n.Children, out)
			for _, h := range n.Handlers {
				if h.Func != nil {
					collectCalleeEdges(h.Func.Block, out)
				}
			}
		case *ir.If:
			collectCalleeEdges(n.Body, out)
			collectCalleeEdges(n.Else, out)
		case *ir.For:
			collectCalleeEdges(n.Body, out)
			collectCalleeEdges(n.Else, out)
		case *ir.PlatformFilter:
			collectCalleeEdges(n.Body, out)
		case *ir.SlotInst:
			collectCalleeEdges(n.Children, out)
		case *ir.ErrorBoundary:
			collectCalleeEdges(n.Children, out)
			if n.Handler != nil && n.Handler.Func != nil {
				collectCalleeEdges(n.Handler.Func.Block, out)
			}
		}
	}
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

// renameIdents rewrites every Ident.Name whose Sym is in renames. Sym
// stays pointing at the original decl so dataflow/reactivity passes can
// still resolve. Mutates exprs in place; caller passes a deep clone.
func renameIdents(stmts []ir.Stmt, renames map[ir.Symbol]string) []ir.Stmt {
	if len(renames) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return e
		}
		if newName, ok2 := renames[id.Sym]; ok2 {
			id.Name = newName
		}
		return e
	})
	return w.stmts(stmts)
}

func renameInExpr(e ir.Expr, renames map[ir.Symbol]string) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = renameIdents(tmp, renames)
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
	if comp == nil || comp == st.main {
		return false
	}
	if st.cycles[comp] {
		return false
	}
	if comp.Native != nil {
		return false
	}
	// Platform-resolved and stdlib wrappers with no body cannot be inlined.
	if len(comp.Body) == 0 && len(comp.Vars) == 0 && len(comp.Funcs) == 0 && len(comp.Timers) == 0 {
		return false
	}
	// Only inline components declared in this package.
	if !st.isLocalComponent(comp) {
		return false
	}
	return true
}

// isLocalComponent reports whether comp is declared in the package being lowered.
func (st *inlineCompState) isLocalComponent(comp *ir.Component) bool {
	return slices.Contains(st.pkg.Components, comp)
}

func (st *inlineCompState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, bool, error) {
	changed := false
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		repl, ch, err := st.inlineStmt(s)
		if err != nil {
			return nil, false, err
		}
		changed = changed || ch
		out = append(out, repl...)
	}
	return out, changed, nil
}

func (st *inlineCompState) inlineStmt(s ir.Stmt) ([]ir.Stmt, bool, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		ch, chCh, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		anyHandlerCh := false
		for _, h := range n.Handlers {
			if h.Func == nil {
				continue
			}
			hbody, hCh, err := st.inlineStmts(h.Func.Block)
			if err != nil {
				return nil, false, err
			}
			h.Func.Block = hbody
			anyHandlerCh = anyHandlerCh || hCh
		}
		if !st.inlinable(n.Component) {
			return []ir.Stmt{n}, chCh || anyHandlerCh, nil
		}
		spliced, err := st.expandCall(n)
		if err != nil {
			return nil, false, err
		}
		return spliced, true, nil
	case *ir.If:
		body, ch1, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.For:
		body, ch1, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.PlatformFilter:
		body, ch, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		return []ir.Stmt{n}, ch, nil
	case *ir.SlotInst:
		ch, chCh, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		return []ir.Stmt{n}, chCh, nil
	case *ir.ErrorBoundary:
		ch, chCh, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		hCh := false
		if n.Handler != nil && n.Handler.Func != nil {
			body, b, err := st.inlineStmts(n.Handler.Func.Block)
			if err != nil {
				return nil, false, err
			}
			n.Handler.Func.Block = body
			hCh = b
		}
		return []ir.Stmt{n}, chCh || hCh, nil
	}
	return []ir.Stmt{s}, false, nil
}

func (st *inlineCompState) expandCall(n *ir.NodeInst) ([]ir.Stmt, error) {
	comp := n.Component
	suffix := st.freshSuffix()

	renames := map[ir.Symbol]string{}

	varStart := len(st.main.Vars)
	for _, v := range comp.Vars {
		clone := cloneVarShallow(v)
		clone.Name = v.Name + suffix
		clone.Init = deepCloneExpr(v.Init)
		renames[v] = clone.Name
		st.main.Vars = append(st.main.Vars, clone)
	}
	funcStart := len(st.main.Funcs)
	for _, f := range comp.Funcs {
		clone := cloneFuncShallow(f)
		clone.Name = f.Name + suffix
		clone.Block = deepCloneStmts(f.Block)
		renames[f] = clone.Name
		st.main.Funcs = append(st.main.Funcs, clone)
	}
	timerStart := len(st.main.Timers)
	for _, t := range comp.Timers {
		clone := *t
		clone.Interval = deepCloneExpr(t.Interval)
		clone.Enabled = deepCloneExpr(t.Enabled)
		if t.Handler != nil {
			h := *t.Handler
			h.Block = deepCloneStmts(t.Handler.Block)
			clone.Handler = &h
		}
		st.main.Timers = append(st.main.Timers, &clone)
	}

	// Apply renames to every hoisted block.
	for i := varStart; i < len(st.main.Vars); i++ {
		if st.main.Vars[i].Init != nil {
			st.main.Vars[i].Init = renameInExpr(st.main.Vars[i].Init, renames)
		}
	}
	for i := funcStart; i < len(st.main.Funcs); i++ {
		st.main.Funcs[i].Block = renameIdents(st.main.Funcs[i].Block, renames)
	}
	for i := timerStart; i < len(st.main.Timers); i++ {
		t := st.main.Timers[i]
		if t.Handler != nil {
			t.Handler.Block = renameIdents(t.Handler.Block, renames)
		}
	}

	body := deepCloneStmts(comp.Body)
	body = renameIdents(body, renames)

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
	for i := varStart; i < len(st.main.Vars); i++ {
		st.main.Vars[i].Init = substituteParamsExpr(st.main.Vars[i].Init, bindings)
	}
	for i := funcStart; i < len(st.main.Funcs); i++ {
		st.main.Funcs[i].Block = substituteParams(st.main.Funcs[i].Block, bindings)
	}
	for i := timerStart; i < len(st.main.Timers); i++ {
		t := st.main.Timers[i]
		t.Interval = substituteParamsExpr(t.Interval, bindings)
		t.Enabled = substituteParamsExpr(t.Enabled, bindings)
		if t.Handler != nil {
			t.Handler.Block = substituteParams(t.Handler.Block, bindings)
		}
	}

	body = substituteSlots(body, n.Children)
	body = substituteEvents(body, n.Handlers)

	if n.ID != "" {
		for _, s := range body {
			if ni, ok := s.(*ir.NodeInst); ok {
				ni.ID = n.ID
				break
			}
		}
	}
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
