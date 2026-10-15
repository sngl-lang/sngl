package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// A test reaches into a component the one under test renders by its `#id`:
// `c.b.hits`, `c.b.btn.click()`, `c.b.reset()`. Every target's test reaches
// the instance under test's own members by name, and a component the inliner
// spliced into it has none left -- its vars and funcs were cloned into the
// owner under names of their own (`hits__inst0`), and its nodes into the
// owner's body. So each such read is pointed at what the splice made of it:
// the var's clone, read as the instance's own state; the node, selected off
// the instance by the id it now carries; the method's clone, called as the
// instance's own. That is what repointCellReads does for an implicit cell and
// repointHandleCalls for a program's method call, said of a test's selects.
//
// A component built at run time is not spliced and has no one instance a
// select could name, so a test reaching into one is refused.

// splicedInstance is what one spliced node carrying an id became.
type splicedInstance struct {
	comp  *ir.Component
	vars  map[string]*ir.Var
	funcs map[*ir.Func]*ir.Func
	// nodes is the handle of each of the body's own nodes by the id it was
	// written with, those a `for` repeats included: `c.b.row[1]` indexes the
	// copies. A handle rather than the node, because a spliced body may be
	// cloned again -- a population is, where the body inserts it -- and every
	// clone shares the handle, which a rename of the id renames too.
	nodes map[string]*ir.Var
}

func (st *inlineCompState) recordInstance(n *ir.NodeInst, symRenames map[ir.Symbol]ir.Symbol, body []ir.Stmt) {
	si := &splicedInstance{comp: n.Component, vars: map[string]*ir.Var{}, funcs: map[*ir.Func]*ir.Func{}}
	for from, to := range symRenames {
		switch f := from.(type) {
		case *ir.Var:
			if t, ok := to.(*ir.Var); ok {
				si.vars[f.Name] = t
			}
		case *ir.Func:
			if t, ok := to.(*ir.Func); ok {
				si.funcs[f] = t
			}
		}
	}
	si.nodes = bodyNodes(body)
	st.insts = append(st.insts, splice{handle: n.Handle, inst: si})
}

// splice is one splice of a component, and the handle its call site gave it.
type splice struct {
	handle *ir.Var
	inst   *splicedInstance
}

// bodyNodes is the handle of each node of a body carrying one, by its id, as
// far as the body's own view reaches: a component it renders is a node, and
// what that renders is that component's.
func bodyNodes(stmts []ir.Stmt) map[string]*ir.Var {
	out := map[string]*ir.Var{}
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok && n.ID != "" && n.Handle != nil {
			if _, seen := out[n.ID]; !seen {
				out[n.ID] = n.Handle
			}
		}
		return true
	})
	return out
}

// testReach is each test's instance and, per component a test is handed,
// its body's nodes as they were written, read before the splice replaces
// them.
//
// A build rooted at the component under test -- `sngl test`'s, one per
// component -- holds the instance's own members under their own names. A
// build of the whole program holds the instance where the program renders it:
// spliced like any other, and reached through what the splice made of it,
// which is one instance only where the program renders the component once.
type testReach struct {
	params map[*ir.Param]bool
	root   *ir.Component
	nodes  map[*ir.Component]map[string]*ir.Var
}

func testReachRoots(pkg *ir.Package, root *ir.Component, opts Options) *testReach {
	r := &testReach{params: testInstances(pkg, opts), root: root, nodes: map[*ir.Component]map[string]*ir.Var{}}
	for p := range r.params {
		if c, ok := p.Type.Decl.(*ir.Component); ok && c == root && r.nodes[c] == nil {
			r.nodes[c] = bodyNodes(c.Stmts)
		}
	}
	return r
}

// takes reports whether a test is handed an instance of comp in a build that
// renders it rather than roots at it.
func (r *testReach) takes(comp *ir.Component) bool {
	if r == nil || comp == r.root {
		return false
	}
	for p := range r.params {
		if p.Type.Decl == comp {
			return true
		}
	}
	return false
}

// resolver answers what a test's select chain names.
type resolver struct {
	*testReach
	// byHandle is every splice of a node by its handle, and rendered every
	// splice of a component a test takes in a build that renders it.
	byHandle map[*ir.Var][]*splicedInstance
	rendered map[*ir.Component][]*splicedInstance
}

// testParam is the test's instance e is, or nil.
func (r *resolver) testParam(e ir.Expr) *ir.Param {
	if id, ok := e.(*ir.Ident); ok {
		if p, ok := id.Sym.(*ir.Param); ok && r.params[p] {
			return p
		}
	}
	return nil
}

// instOf is what the splice made of the instance e names, or nil: for the
// root, whose members keep their names, and for anything not spliced.
func (r *resolver) instOf(e ir.Expr) *splicedInstance {
	e = unnarrowed(e)
	if p := r.testParam(e); p != nil {
		if c, _ := p.Type.Decl.(*ir.Component); c != nil && c != r.root {
			if copies := r.rendered[c]; len(copies) == 1 {
				return copies[0]
			}
		}
		return nil
	}
	if sel, ok := e.(*ir.Select); ok {
		if copies := r.byHandle[r.nodeIn(sel.Operand, sel.Field)]; len(copies) == 1 {
			return copies[0]
		}
	}
	return nil
}

// nodeIn is the handle of the node the instance x renders under the id, or
// nil.
func (r *resolver) nodeIn(x ir.Expr, id string) *ir.Var {
	if si := r.instOf(x); si != nil {
		return si.nodes[id]
	}
	if p := r.testParam(x); p != nil {
		if c, _ := p.Type.Decl.(*ir.Component); c != nil {
			return r.nodes[c][id]
		}
	}
	return nil
}

// instance is the test's instance a select chain is rooted at.
func (r *resolver) instance(e ir.Expr) *ir.Ident {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Conversion:
			e = x.Operand
		case *ir.Ident:
			if r.testParam(x) != nil {
				return x
			}
			return nil
		default:
			return nil
		}
	}
}

func repointTestReaches(pkg *ir.Package, reach *testReach, splices []splice) error {
	if len(reach.params) == 0 {
		return nil
	}
	r := &resolver{testReach: reach, byHandle: map[*ir.Var][]*splicedInstance{}, rendered: map[*ir.Component][]*splicedInstance{}}
	for _, sp := range splices {
		if sp.handle != nil {
			r.byHandle[sp.handle] = append(r.byHandle[sp.handle], sp.inst)
		}
		if reach.takes(sp.inst.comp) {
			r.rendered[sp.inst.comp] = append(r.rendered[sp.inst.comp], sp.inst)
		}
	}
	// Only a build rooted at a test's component is one its tests run
	// against. A build of the whole program emits no test, so a read it cannot
	// answer is left for the build that roots at the component to refuse.
	var bad error
	fail := func(e ir.Expr, format string, args ...any) {
		if bad == nil && reach.root != nil {
			bad = fmt.Errorf("%s: %s", exprPos(e), fmt.Sprintf(format, args...))
		}
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
			switch x := n.(type) {
			case *ir.Call:
				r.repointCall(x, fail)
			case *ir.Select:
				si := r.instOf(x.Operand)
				if si == nil {
					r.refuseUnspliced(x.Operand, x.Field, fail)
					return n, nil
				}
				if v := si.vars[x.Field]; v != nil {
					return &ir.Ident{Name: v.Name, Type: x.Type, Sym: v}, ir.SkipDir
				}
				if h := si.nodes[x.Field]; h != nil && len(r.byHandle[h]) == 0 {
					return &ir.Select{AST: x.AST, Type: x.Type, Operand: r.instance(x), Field: h.Name}, ir.SkipDir
				}
			}
			return n, nil
		})
	}
	return bad
}

// repointCall points a method called through a spliced instance at the
// method's clone, called as the instance's own.
func (r *resolver) repointCall(c *ir.Call, fail func(ir.Expr, string, ...any)) {
	if c.Func == nil || c.Event != "" {
		return
	}
	recv, asArg := c.Receiver, false
	if recv == nil && len(c.Args) > 0 && len(c.Func.Params) > 0 && c.Func.Params[0].Receiver {
		recv, asArg = c.Args[0].Value, true
	}
	if recv == nil {
		return
	}
	si := r.instOf(recv)
	if si == nil {
		r.refuseUnspliced(recv, c.Func.Name, fail)
		return
	}
	clone := si.funcs[c.Func]
	if clone == nil {
		return
	}
	c.Func = clone
	// A clone spliced into the package body is no method of anything any
	// more (dropReceiver), and is called with nothing in the receiver's place.
	if len(clone.Params) == 0 || !clone.Params[0].Receiver {
		c.Receiver = nil
		if asArg {
			c.Args = slices.Delete(slices.Clone(c.Args), 0, 1)
		}
		return
	}
	inst := r.instance(recv)
	if asArg {
		c.Args[0].Value = inst
	} else {
		c.Receiver = inst
	}
}

// refuseUnspliced reports a test reaching through x for a member of a
// component nothing spliced: one built at run time, or one the program
// renders more than once, neither of which a select can name one instance of.
func (r *resolver) refuseUnspliced(x ir.Expr, member string, fail func(ir.Expr, string, ...any)) {
	comp := componentOf(x)
	x = unnarrowed(x)
	if comp == nil || comp == r.root || !reachesInto(comp, member) {
		return
	}
	if r.testParam(x) != nil {
		return
	}
	sel, ok := x.(*ir.Select)
	if !ok || r.nodeIn(sel.Operand, sel.Field) == nil {
		return
	}
	if len(r.byHandle[r.nodeIn(sel.Operand, sel.Field)]) > 1 {
		fail(x, "`#%s` is rendered more than once, so a test cannot say which copy's %s it means; give each copy its own id", sel.Field, member)
		return
	}
	fail(x, "`#%s` is built at run time on this target, so there is no one instance whose %s a test can reach", sel.Field, member)
}

// reachesInto reports whether member names something comp declares that a
// splice would have moved: a var, a func or a node its body renders.
func reachesInto(comp *ir.Component, member string) bool {
	if len(comp.Stmts) == 0 {
		return false
	}
	for _, v := range comp.Vars {
		if v.Name == member {
			return true
		}
	}
	for _, f := range comp.Funcs {
		if f.Name == member {
			return true
		}
	}
	_, ok := bodyNodes(comp.Stmts)[member]
	return ok
}

func exprPos(e ir.Expr) string {
	switch x := e.(type) {
	case *ir.Select:
		if x.AST != nil {
			return x.AST.Pos.String()
		}
	case *ir.Ident:
		if x.AST != nil {
			return x.AST.Pos.String()
		}
	}
	return "<unknown>"
}

// unnarrowed is e without the conversion a null test narrows an option ref
// by, which names the same instance.
func unnarrowed(e ir.Expr) ir.Expr {
	if conv, ok := e.(*ir.Conversion); ok {
		return conv.Operand
	}
	return e
}
