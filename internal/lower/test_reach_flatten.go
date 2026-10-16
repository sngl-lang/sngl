package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// passTestReach makes every node a test reaches through an instance a node of
// the test's own instance: `c.b.hit` becomes `c.hit`, and `c.r[1].hit`
// becomes `c.hit[1]`, the indices of each step kept in order -- which is the
// copy key a node inside an instance built at run time already carries, its
// instance's key extended by its own loops (ir.CopyProp). So a trigger, a
// null test or a node handed to sngl:test/browser is one every target
// answers the way it answers a node of the instance itself: by the id, and
// for one copy by the key.
//
// That holds only where the id names one node in the build, so a node
// reached this way is renamed where another node carries its id, and a
// component rendered at more than one site is copied for the site a test
// reaches into, so the copy's nodes are that site's alone.
//
// First after TestInstanceReach, before anything moves a node out of the body
// that wrote it.
var passTestReach = pass{
	name:    "TestReach",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		if pkg == nil {
			return nil
		}
		params := testInstances(pkg, opts)
		if len(params) == 0 {
			return nil
		}
		f := &reachFlattener{pkg: pkg, params: params, names: namesOrOwn(opts.names), uids: map[*ir.NodeInst]string{}, copied: map[*ir.Component]bool{}, fires: map[fireKey]*ir.Func{}, owners: map[*ir.NodeInst]*ir.Component{}}
		f.index(reachedBodies(renderedBodies(pkg, opts), rootComponent(pkg, opts)))
		for _, fn := range pkg.Funcs {
			if fn == nil || !fn.IsTest {
				continue
			}
			if err := answerTestPropReads(fn, params); err != nil {
				return err
			}
			if err := f.fireTriggers(fn); err != nil {
				return err
			}
			if err := f.rewrite(fn); err != nil {
				return err
			}
		}
		f.applyRenames()
		return nil
	},
}

type reachFlattener struct {
	pkg    *ir.Package
	params map[*ir.Param]bool
	names  renamer
	// sites is each component's instantiations across the bodies the build
	// renders, and ids each node carrying an id there.
	sites map[*ir.Component][]*ir.NodeInst
	ids   map[string][]*ir.NodeInst
	// uids is the id each node a test reaches is reached under, and copied
	// each component copied for one site (siteComponent).
	uids   map[*ir.NodeInst]string
	copied map[*ir.Component]bool
	// fires is the function each event a test fires on a call site is a call
	// of (fireTriggers), and owners the component whose body wrote each node
	// a test reaches.
	fires  map[fireKey]*ir.Func
	owners map[*ir.NodeInst]*ir.Component
	// bodies is every rendered body, for the renames.
	bodies []renderedBody
}

// reachedBodies is the bodies a build rooted at root renders, every one where
// it roots at none: a component it does not render is no site of anything.
func reachedBodies(bodies []renderedBody, root *ir.Component) []renderedBody {
	if root == nil {
		return bodies
	}
	byOwner := map[*ir.Component]renderedBody{}
	for _, b := range bodies {
		if b.owner != nil {
			byOwner[b.owner] = b
		}
	}
	seen := map[*ir.Component]bool{root: true}
	queue := []*ir.Component{root}
	var out []renderedBody
	for len(queue) > 0 {
		b, ok := byOwner[queue[0]]
		queue = queue[1:]
		if !ok {
			continue
		}
		out = append(out, b)
		for _, r := range b.roots {
			_ = ir.Walk(r, func(n ir.Node) error {
				if ni, ok := n.(*ir.NodeInst); ok && ni.Component != nil && !seen[ni.Component] {
					seen[ni.Component] = true
					queue = append(queue, ni.Component)
				}
				return nil
			})
		}
	}
	return out
}

func (f *reachFlattener) index(bodies []renderedBody) {
	f.bodies = bodies
	f.sites = map[*ir.Component][]*ir.NodeInst{}
	f.ids = map[string][]*ir.NodeInst{}
	seen := map[*ir.NodeInst]bool{}
	for _, b := range bodies {
		for _, r := range b.roots {
			_ = ir.Walk(r, func(n ir.Node) error {
				ni, ok := n.(*ir.NodeInst)
				if !ok || seen[ni] {
					return nil
				}
				seen[ni] = true
				if ni.Component != nil {
					f.sites[ni.Component] = append(f.sites[ni.Component], ni)
				}
				if ni.ID != "" {
					f.ids[ni.ID] = append(f.ids[ni.ID], ni)
				}
				return nil
			})
		}
	}
}

// reached is a node ref a test makes, as one step off the test's instance.
type reached struct {
	param *ir.Ident
	field string
	ref   ir.RefCount
	idxs  []ir.Expr
	node  *ir.NodeInst
	// steps is how many instances the ref was reached through.
	steps int
}

func (f *reachFlattener) testParam(e ir.Expr) *ir.Ident {
	if id, ok := e.(*ir.Ident); ok {
		if p, ok := id.Sym.(*ir.Param); ok && f.params[p] {
			return id
		}
	}
	return nil
}

// ref reads e as a node ref rooted at the test's instance, or reports false.
func (f *reachFlattener) ref(e ir.Expr) (*reached, bool, error) {
	e = unnarrowed(e)
	var idxs []ir.Expr
	for ix, ok := e.(*ir.Index); ok; ix, ok = e.(*ir.Index) {
		idxs = append([]ir.Expr{ix.Idx}, idxs...)
		e = unnarrowed(ix.Operand)
	}
	sel, ok := e.(*ir.Select)
	if !ok || sel.Ref == ir.RefNone || sel.Populated {
		return nil, false, nil
	}
	comp := componentOf(sel.Operand)
	if comp == nil {
		return nil, false, nil
	}
	if p := f.testParam(sel.Operand); p != nil {
		node, _ := handleNode(comp.Stmts, sel.Field)
		return &reached{param: p, field: sel.Field, ref: sel.Ref, idxs: idxs, node: node, steps: 1}, true, nil
	}
	parent, ok, err := f.ref(sel.Operand)
	if !ok || err != nil || parent.node == nil || parent.node.Component == nil {
		return nil, false, err
	}
	comp, err = f.siteComponent(sel, parent.node)
	if err != nil {
		return nil, false, err
	}
	node, _ := handleNode(comp.Stmts, sel.Field)
	if node == nil || inSlotFallback(comp.Stmts, node) {
		return nil, false, nil
	}
	uid := f.uid(node)
	f.owners[node] = comp
	return &reached{
		param: parent.param,
		field: uid,
		ref:   composeRef(parent.ref, sel.Ref),
		idxs:  append(append([]ir.Expr{}, parent.idxs...), idxs...),
		node:  node,
		steps: parent.steps + 1,
	}, true, nil
}

// siteComponent is the component the instance site renders, as a test
// reaches into it: a copy of its own where the program renders the component
// at more than one site, so the nodes a test reaches through this one are
// this one's alone.
func (f *reachFlattener) siteComponent(at *ir.Select, site *ir.NodeInst) (*ir.Component, error) {
	comp := site.Component
	if f.copied[comp] || len(f.sites[comp]) <= 1 {
		return comp, nil
	}
	if comp.PkgPath() != "" {
		return nil, fmt.Errorf("%s: a test reaches into %s, which the program renders at %d sites and another package declares; give the site a component of the program's own", exprPos(at), comp.Name, len(f.sites[comp]))
	}
	c := cloneComponent(comp, f.names.next(comp.Name, "site"), comp.Stmts)
	c.RuntimeInstance = comp.RuntimeInstance
	handles := map[ir.Symbol]ir.Symbol{}
	_ = ir.Walk(c.Stmts, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.Handle != nil {
			h := *ni.Handle
			handles[ni.Handle] = &h
			ni.Handle = &h
		}
		return nil
	})
	c.Stmts = renameIdents(c.Stmts, nil, handles)
	for _, fn := range c.Funcs {
		fn.Block = renameIdents(fn.Block, nil, handles)
	}
	for _, v := range c.Vars {
		v.Init = renameInExpr(v.Init, nil, handles)
	}
	f.pkg.Components = append(f.pkg.Components, c)
	f.copied[c] = true
	site.Component, site.Name = c, c.Name
	return c, nil
}

// uid is the id node is reached under from the test's instance: its own
// where nothing else in the build carries it.
func (f *reachFlattener) uid(node *ir.NodeInst) string {
	if uid, ok := f.uids[node]; ok {
		return uid
	}
	uid := node.ID
	if slices.ContainsFunc(f.ids[node.ID], func(n *ir.NodeInst) bool { return n != node }) {
		uid = f.names.next(node.ID, "reach")
	}
	f.uids[node] = uid
	return uid
}

// answerTestPropReads answers each one-way prop fn reads through its instance
// (rewriteTestPropRead), to a fixed point: an answer may read another prop.
// Before the flattening, which leaves a node no path to its call site.
func answerTestPropReads(fn *ir.Func, params map[*ir.Param]bool) error {
	for {
		changed := false
		if err := ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
			sel, ok := n.(*ir.Select)
			if !ok {
				return n, nil
			}
			if e := rewriteTestPropRead(sel, params); e != nil {
				changed = true
				return e, ir.SkipDir
			}
			return n, nil
		}); err != nil {
			return err
		}
		if !changed {
			return nil
		}
	}
}

// fireTriggers makes each event a test fires on an instance of a program
// component -- `c.one.pressed(5)`, `c.e[1].pressed(7)` -- a call of what the
// instance's call site handles it with, made a function of the component the
// call site is written in, `__fire_one_pressed(n int)`: the component emits
// its events from the program's own code, so a test firing one stands in for
// that emit. The function binds each loop around the call site to the copy
// the test indexed, an index it is handed after the event's arguments, and is
// reached through the instance chain as any method is.
func (f *reachFlattener) fireTriggers(fn *ir.Func) error {
	var bad error
	_ = ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
		c, ok := n.(*ir.Call)
		if !ok || c.Event == "" || c.Receiver == nil || bad != nil {
			return n, nil
		}
		inst, id, idxs, ok := testNodeRef(c.Receiver, f.params)
		if !ok {
			return n, nil
		}
		owner := componentOf(inst)
		if owner == nil {
			return n, nil
		}
		site, loops := nodeUnderLoops(owner.Stmts, id)
		if site == nil || site.Component == nil || site.Component.PkgPath() != "" || site.Component.Primitive() || len(loops) != len(idxs) {
			return n, nil
		}
		var handler *ir.Func
		for _, h := range site.Handlers {
			if h.Name == c.Event && h.Func != nil {
				handler = h.Func
			}
		}
		if handler == nil {
			return n, nil
		}
		fire, err := f.fireFunc(c, owner, site, handler, loops)
		if err != nil {
			bad = err
			return n, nil
		}
		args := make([]ir.CallArg, 0, len(handler.Params)+len(idxs)+1)
		args = append(args, ir.CallArg{Value: inst})
		for i, p := range handler.Params {
			if i < len(c.Args) {
				args = append(args, ir.CallArg{Value: c.Args[i].Value})
			} else {
				args = append(args, ir.CallArg{Value: ir.DeclaredDefault(p.Type)})
			}
		}
		for _, idx := range idxs {
			args = append(args, ir.CallArg{Value: idx})
		}
		return &ir.Call{Type: ir.TypVoid, Func: fire, Args: args}, ir.SkipDir
	})
	return bad
}

// fireFunc is the function of owner a test calls to fire event on site, once
// per site and event.
func (f *reachFlattener) fireFunc(at *ir.Call, owner *ir.Component, site *ir.NodeInst, handler *ir.Func, loops []*ir.For) (*ir.Func, error) {
	key := fireKey{site, at.Event}
	if fn, ok := f.fires[key]; ok {
		return fn, nil
	}
	syms := map[ir.Symbol]ir.Symbol{}
	params := make([]*ir.Param, 0, len(handler.Params)+len(loops)+1)
	params = append(params, &ir.Param{Name: "this", Type: owner.SymType(), Receiver: true})
	for _, p := range handler.Params {
		cp := *p
		syms[p] = &cp
		params = append(params, &cp)
	}
	idxs := make([]ir.Expr, len(loops))
	for i := range loops {
		p := &ir.Param{Name: fmt.Sprintf("__copy%d", i), Type: ir.TypInt}
		params = append(params, p)
		idxs[i] = &ir.Ident{Name: p.Name, Type: p.Type, Sym: p}
	}
	env := loopBindings(loops, idxs)
	block := renameIdents(deepCloneStmts(handler.Block), nil, syms)
	unbound := ""
	block = newExprWalker(func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok {
			return e
		}
		lv, isLoop := id.Sym.(*ir.LoopVar)
		if !isLoop {
			return e
		}
		if bound, ok := env[lv]; ok {
			return ir.CloneExprSharingDecls(bound)
		}
		if slices.ContainsFunc(loops, func(l *ir.For) bool { return l.KeySym == lv || l.ValueSym == lv }) && unbound == "" {
			unbound = lv.Name
		}
		return e
	}).stmts(block)
	if unbound != "" {
		return nil, fmt.Errorf("%s: a test fires @%s on `#%s`, whose handler reads `%s` of a loop over something other than a list, which a test's index names no element of", handleCallPos(at), at.Event, writtenID(site), unbound)
	}
	fn := *handler
	fn.Name = f.names.next("__fire_"+site.ID+"_"+at.Event, "t")
	fn.Params = params
	fn.Block = block
	fn.Return = ir.TypVoid
	fn.Receiver = owner.SymType()
	fire := &fn
	owner.Funcs = append(owner.Funcs, fire)
	if owner.Methods != nil {
		owner.Methods[fire.Name] = fire
	}
	f.fires[key] = fire
	return fire, nil
}

type fireKey struct {
	site  *ir.NodeInst
	event string
}

// composeRef is the count a node with inner's wrappers, inside a node with
// outer's, is reached under from outer's instance.
func composeRef(outer, inner ir.RefCount) ir.RefCount {
	k := inner
	for i := len(outer) - 2; i >= 0; i-- {
		k = ir.RefCount(string(outer[i]) + string(k))
	}
	return k
}

func (f *reachFlattener) rewrite(fn *ir.Func) error {
	var bad error
	_ = ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
		if bad != nil {
			return n, ir.SkipDir
		}
		e, ok := n.(ir.Expr)
		if !ok {
			return n, nil
		}
		switch e.(type) {
		case *ir.Select, *ir.Index, *ir.Conversion:
		default:
			return n, nil
		}
		r, ok, err := f.ref(e)
		if err != nil {
			bad = err
			return n, ir.SkipDir
		}
		if !ok || r.node == nil || r.steps == 1 {
			return n, nil
		}
		return f.spell(r, e.ExprType()), ir.SkipDir
	})
	return bad
}

// spell is r written as a select off the test's instance and its indices,
// narrowed to want where the select's count is wider.
func (f *reachFlattener) spell(r *reached, want *ir.Type) ir.Expr {
	nodeType := r.node.Component.SymType()
	var out ir.Expr = &ir.Select{Type: r.ref.Wrap(nodeType, ir.ListOf), Operand: r.param, Field: r.field, Ref: r.ref}
	for _, idx := range r.idxs {
		t := out.ExprType()
		elem := t
		if t != nil && t.Kind == ir.TypeList && len(t.Elems) == 1 {
			elem = t.Elems[0]
		}
		out = &ir.Index{Type: elem, Operand: out, Idx: idx}
	}
	if t := out.ExprType(); want != nil && t != nil && !t.Equal(want) {
		out = &ir.Conversion{Type: want, Operand: out}
	}
	return out
}

// handleNode is the node a body writes under the id, as far as its own view
// reaches.
func handleNode(stmts []ir.Stmt, id string) (*ir.NodeInst, bool) {
	var out *ir.NodeInst
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok && n.ID == id && out == nil {
			out = n
		}
		return out == nil
	})
	return out, out != nil
}

// inSlotFallback reports whether node is written in the fallback of a slot
// insertion, which a splice drops where the call site fills the slot: the
// splice's record answers a test's reach of it (test_reach.go's dropped).
func inSlotFallback(stmts []ir.Stmt, node *ir.NodeInst) bool {
	found := false
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if si, ok := s.(*ir.SlotInst); ok && !found {
			ir.WalkView(si.Children, func(c ir.Stmt) bool {
				if c == ir.Stmt(node) {
					found = true
				}
				return !found
			})
		}
		return !found
	})
	return found
}

// applyRenames gives each node reached under an id of its own that id, the
// handle's reads following, and so does a select of it through an instance of
// the component that wrote it -- a program's own, a test's being flat by now.
// A copy's nodes are selected through no value of the copy's type.
func (f *reachFlattener) applyRenames() {
	var reads map[*ir.Var][]*ir.Ident
	members := map[*ir.Component]map[string]string{}
	for n, uid := range f.uids {
		if comp := f.owners[n]; n.ID != uid && comp != nil && !f.copied[comp] {
			if members[comp] == nil {
				members[comp] = map[string]string{}
			}
			members[comp][n.ID] = uid
		}
	}
	defer func() {
		if len(members) == 0 {
			return
		}
		_ = ir.Walk(f.pkg, func(x ir.Node) error {
			if sel, ok := x.(*ir.Select); ok && sel.Ref != ir.RefNone {
				if name, ok := members[componentOf(unnarrowed(sel.Operand))][sel.Field]; ok {
					sel.Field = name
				}
			}
			return nil
		})
	}()
	for n, uid := range f.uids {
		if n.ID == uid {
			continue
		}
		if reads == nil {
			reads = map[*ir.Var][]*ir.Ident{}
			seen := map[*ir.Ident]bool{}
			collect := func(x ir.Node) error {
				if id, ok := x.(*ir.Ident); ok && !seen[id] {
					if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
						seen[id] = true
						reads[v] = append(reads[v], id)
					}
				}
				return nil
			}
			for _, b := range f.bodies {
				for _, r := range b.roots {
					_ = ir.Walk(r, collect)
				}
			}
			_ = ir.Walk(f.pkg, collect)
		}
		renameNodeID(n, uid, reads)
	}
}

// followTestNodeID renames each node a test selects off its own instance
// under was to id: a splice moved the call site's id onto the node, which
// passTestReach had already made one of the test's instance's own, under an
// id no other node carries.
func followTestNodeID(pkg *ir.Package, was, id string) {
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			sel, ok := n.(*ir.Select)
			if !ok || sel.Ref == ir.RefNone || sel.Field != was {
				return nil
			}
			if p, ok := sel.Operand.(*ir.Ident); ok {
				if param, ok := p.Sym.(*ir.Param); ok && slices.Contains(fn.Params, param) {
					sel.Field = id
				}
			}
			return nil
		})
	}
}
