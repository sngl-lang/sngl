package interp

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// maxRenderDepth bounds how deeply one mount may nest component
// instantiations: a recursive component whose base case never arrives would
// otherwise recurse here until the Go stack gives out.
//
// It is lower.MaxRecursionDepth rather than a number of its own so the two
// cannot drift. The interpreter does not lower, so the `if __depth >= N` that
// pass writes into every compiled target never reaches it -- this is the same
// bound, stated the only way an interpreter can state one. It counts every
// nested component and not only the members of a cycle, which is stricter than
// the pass by however many wrappers stand between two levels of the recursion;
// on a program that is not recursing at all, nothing comes near either.
//
// It replaced a silent truncation at a depth of 100, which returned the tree
// the bound had cut short and told nobody: below that point the program simply
// was not there.
const maxRenderDepth = lower.MaxRecursionDepth

// recursionExhausted is the error a mount that hit the bound returns. A
// RaisedError, so the nearest enclosing boundary catches it the way it
// catches error.raise, and the kind matches the one the lowering pass writes.
func recursionExhausted(name string) *RaisedError {
	return &RaisedError{Event: map[string]any{
		"message": fmt.Sprintf("%s: recursion exceeded %d nested instances", name, maxRenderDepth),
		"kind":    "recursion",
	}}
}

// View is the interpreter's retained render tree, mounted once and patched.
type View struct {
	// Roots are the mounted top-level nodes, in source order.
	Roots []*Node
	// byKey indexes every node by its mounted path.
	byKey map[Key]*Node
	// byID indexes by #id. A `for` renders one node per iteration, so an id
	// inside a loop names several.
	byID map[string][]*Node
	// owner is the instance the tree was mounted for, whose refs Refs answers.
	owner *Env
	// iterations is every loop iteration and list-slot insertion mounted, in
	// mount order, so a ref through a loop that rendered nothing in one
	// iteration still has an entry for it.
	iterations []iteration
	// Effects are the lifetime brackets this tree holds, in mount order. Not
	// among the nodes: an effect draws nothing, and what it wants from the
	// tree is only the lifetime that reaching it at all confers.
	Effects []MountedEffect
	// Timers are the recurring deadlines this tree describes, in mount order.
	// Held here for the reason Effects are: a schedule renders nothing, and
	// what it takes from the tree is only the position it was reached at.
	Timers []MountedTimer
}

// Node is one retained instance in a View.
type Node struct {
	// Key is the mounted path, crossing component instantiations: one
	// declaration mounted twice is two nodes, so ComponentKeys is not enough.
	Key Key
	// Name is the resolved element name, ID the #id binding if any.
	Name string
	ID   string
	// Props are the evaluated prop values, and PropOrder the order they were
	// written in -- both the snapshot printer and the emitted platforms depend
	// on that order, so a map alone will not do.
	Props     map[string]any
	PropOrder []string
	// Handlers are the declared events, in declaration order. Ordered rather
	// than mapped because a snapshot prints them and has to be deterministic.
	Handlers []Handler
	Children []*Node
	// Env is the scope the props were evaluated in. Held because a handler
	// runs in it, and because a re-evaluation has to use the same scope the
	// mount did.
	Env *Env
	// Inst is the IR this node was mounted from, or nil for the
	// children-less call form. Repointed by a reload; never used as identity.
	Inst *ir.NodeInst
	// CompEnv is the component's own scope; Node.Env is the caller's, where the
	// arguments were evaluated.
	CompEnv *Env
	// Expanded says the component's body was mounted beneath this node. A
	// component with an empty visual body renders nothing of its own, so in the
	// rendered tree it IS the element and Find must return it. Not
	// `len(Children) > 0`: `if false { … }` expands to nothing and is still
	// expanded.
	Expanded bool
	// Component is the declaration this node instantiates; its Children are the
	// expansion of that component's body. Kept as a node so one tree serves
	// both readings: a snapshot descends through it, an inspector stops at it.
	Component *ir.Component
	// Owner is the component instance whose body wrote this node, which is
	// the instance whose `#id` refs it answers to: children a caller wrote
	// bare are the caller's wherever the callee inserts them, and so are the
	// nodes of a population the caller wrote, reached through the node it
	// populates.
	Owner *Env
	// Iters names the loop iterations, and list-slot insertions, the node was
	// mounted under within its owner's body, outermost first: what groups a
	// ref through nested loops into a list of lists.
	Iters []string
	// SuppliedTo is the instance whose slot population wrote the node, nil for
	// one its owner's body wrote directly; Base is how many of Iters were
	// already around that instance's call site.
	SuppliedTo *Env
	Base       int
	// Context is the provided values the node was mounted under, which a
	// handler runs with: by the time one fires every provider has unwound.
	Context map[*ir.Context]any
}

// Handler is one declared event on a node. Body is whatever the IR carried:
// an *ir.Func for a visual node, an *ast.StmtBlock for the call form.
type Handler struct {
	Name string
	Body any
}

// IsComponent reports whether this node instantiates a component rather than a
// platform element.
func (n *Node) IsComponent() bool { return n != nil && n.Component != nil }

// IsUserComponent reports whether this instantiates one of the program's own
// components. Not the same question as Expanded, and they differ on one case:
// `component holder { var n = 0 }` renders nothing and a snapshot must skip it,
// while `vbox` resolves to a declaration with nothing in it and must print.
func (n *Node) IsUserComponent() bool {
	return n != nil && IsUserComponent(n.Component)
}

// IsUserComponent reports whether comp is one of the program's own components.
// A stdlib element resolves to a declaration carrying no body, vars or funcs.
func IsUserComponent(comp *ir.Component) bool {
	if comp == nil {
		return false
	}
	return len(comp.Stmts) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0
}

// Mount builds the retained tree for env's component body.
func Mount(env *Env) (*View, error) {
	v := &View{byKey: map[Key]*Node{}, byID: map[string][]*Node{}, owner: env}
	if env == nil || env.BodyStmts == nil {
		return v, nil
	}
	root := ""
	if env.Comp != nil {
		root = env.Comp.Name
	}
	m := &mounter{view: v, root: root, owner: env, reached: map[childKey]bool{}, visited: map[*Env]bool{}}
	nodes, err := m.stmts(env, env.BodyStmts, "")
	if err != nil {
		return nil, err
	}
	m.evictUnreached()
	v.Roots = nodes
	return v, nil
}

// evictUnreached drops the state of every instance a scope this mount
// rendered into held and this mount did not render: a component under an
// `if` that went false, a page its stack is no longer showing. Nothing about
// an unmounted instance survives, which is what every compiled target does
// with it, so mounting it again starts it from its initializers.
func (m *mounter) evictUnreached() {
	for env := range m.visited {
		for key := range env.childEnvs {
			if !m.reached[key] {
				delete(env.childEnvs, key)
			}
		}
	}
}

// OwnedBy is v answering for the nodes owner's body wrote: a read of an
// instance's refs through the tree an ancestor mounted, where the instance's
// slots hold what its call site wrote.
func (v *View) OwnedBy(owner *Env) *View {
	if v == nil {
		return nil
	}
	cp := *v
	cp.owner = owner
	return &cp
}

// Find returns the element nodes carrying the given #id anywhere in the tree,
// in mount order, skipping a component instantiation that carries one. A host
// addresses nodes this way; `c.<id>` is Refs. FindAny is the unfiltered form.
func (v *View) Find(id string) []*Node {
	if v == nil {
		return nil
	}
	var out []*Node
	for _, n := range v.byID[id] {
		if n.IsComponent() && n.Expanded {
			continue
		}
		out = append(out, n)
	}
	return out
}

// FindAny returns every node carrying the given #id, component instantiations
// included.
func (v *View) FindAny(id string) []*Node {
	if v == nil {
		return nil
	}
	return v.byID[id]
}

// Refs returns the nodes carrying #id that the mounted instance's own body
// wrote, component instantiations included, in mount order: what `c.<id>`
// names. A node in a component the instance renders is that component's ref.
// With supplied set, it is the nodes a population the instance wrote supplied
// to that instance instead: `c.card.title`, through `card`.
func (v *View) Refs(id string, supplied *Env) []*Node {
	if v == nil {
		return nil
	}
	var out []*Node
	for _, n := range v.byID[id] {
		if n.Owner == v.owner && n.SuppliedTo == supplied {
			out = append(out, n)
		}
	}
	return out
}

// At returns the node at a mounted path.
func (v *View) At(k Key) (*Node, bool) {
	if v == nil {
		return nil, false
	}
	n, ok := v.byKey[k]
	return n, ok
}

// Len is the number of mounted nodes.
func (v *View) Len() int {
	if v == nil {
		return 0
	}
	return len(v.byKey)
}

// Walk visits every node depth-first in mount order.
func (v *View) Walk(fn func(*Node) bool) {
	if v == nil {
		return
	}
	walkNodes(v.Roots, fn)
}

func walkNodes(nodes []*Node, fn func(*Node) bool) bool {
	for _, n := range nodes {
		if !fn(n) {
			return false
		}
		if !walkNodes(n.Children, fn) {
			return false
		}
	}
	return true
}

// Map renders the node the way ResolveElementRef's element maps are shaped, so
// a caller of that can be moved onto the tree without changing what it reads.
func (n *Node) Map() map[string]any {
	m := make(map[string]any, len(n.Props)+len(n.Handlers)+2)
	m["_type"] = n.Name
	maps.Copy(m, n.Props)
	for _, h := range n.Handlers {
		m["@"+h.Name] = h.Body
	}
	m["__ownerEnv"] = n.Env
	m["__ownerContext"] = n.Context
	if n.Inst != nil {
		m["__inst"] = n.Inst
	}
	if n.CompEnv != nil {
		m["__compEnv"] = n.CompEnv
	}
	m["__node"] = n
	return m
}

// Handling is the first node the view rendered, in mount order, carrying a
// handler for event, or nil.
func (v *View) Handling(event string) *Node {
	if v == nil {
		return nil
	}
	root := &Node{Children: v.Roots}
	return handlingDescendant(root, event)
}

// forwardingDescendant is the node a host reports event through, when n is a
// component whose body hands that event on: the first node under n, in mount
// order, whose handler emits event from n's instance -- the primitive an
// override renders, `Control(@change(on) { checked = on; change(on) })`. The
// handler it carries is returned with it. A trigger enters there rather than
// at n's call site, so the override writes the prop it reports as a host's
// report would make it, and its emit runs the call site's handler.
//
// Only the nodes n's own body rendered are asked: another component's
// instance below them emits that component's events, not n's.
func forwardingDescendant(n *Node, event string) (*Node, string) {
	if n == nil || !n.Expanded {
		return nil, ""
	}
	var find func(ns []*Node) (*Node, string)
	find = func(ns []*Node) (*Node, string) {
		for _, k := range ns {
			for _, h := range k.Handlers {
				if fn, ok := h.Body.(*ir.Func); ok && emitsEvent(fn.Block, event) {
					return k, h.Name
				}
			}
			if k.Expanded {
				continue
			}
			if d, name := find(k.Children); d != nil {
				return d, name
			}
		}
		return nil, ""
	}
	return find(n.Children)
}

// instanceBody is what the component instance a handle map names rendered: n
// itself once its body is mounted beneath it, and otherwise a mount of the
// instance's own scope. A map a test reads off a handle was taken when the
// node was, before the body beneath it was, so the node it carries holds none
// of it.
func instanceBody(m map[string]any, n *Node) *Node {
	if n != nil && n.Expanded {
		return n
	}
	ce, _ := m["__compEnv"].(*Env)
	if ce == nil || len(ce.BodyStmts) == 0 {
		return n
	}
	view, err := Mount(ce)
	if err != nil {
		return n
	}
	return &Node{Children: view.Roots, Expanded: true}
}

// emitsEvent reports whether stmts emit event, at any depth of their blocks.
func emitsEvent(stmts []ir.Stmt, event string) bool {
	found := false
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if e, ok := n.(*ir.Emit); ok && e.Name == event {
			found = true
			return ir.SkipDir
		}
		return nil
	})
	return found
}

// handlingDescendant is the first node under n, in mount order, carrying a
// handler for event: where an event a component declares but its call site
// did not handle comes from. A test clicking a `nav.link` clicks what the
// link renders, as a user would.
func handlingDescendant(n *Node, event string) *Node {
	for _, k := range n.Children {
		for _, h := range k.Handlers {
			if h.Name == event {
				return k
			}
		}
		if d := handlingDescendant(k, event); d != nil {
			return d
		}
	}
	return nil
}

// mounter carries the mount-wide state: the view being filled and the root
// component's name, which every mounted key is scoped to.
type mounter struct {
	view *View
	root string
	// slots is the stack of call sites whose components are currently being
	// expanded. An insertion point renders whatever the frame on top supplied
	// for it, in the scope that frame was written in.
	slots []slotFrame
	// entries is the stack of insertions whose populations are being mounted,
	// which is where an insertion of a component entry finds its content.
	entries []entryFrame
	// owner is the instance whose body the statements being mounted were
	// written in, stamped on each node as its Owner.
	owner *Env
	// iters, supplied and base are what each node mounted now is stamped with
	// (Node.Iters, SuppliedTo, Base).
	iters    []string
	supplied *Env
	base     int
	// navs is the stack of navigators being mounted (nav.go), and byEnv the
	// node each component instance mounted as, by its scope.
	navs  []*navFrame
	byEnv map[*Env]*Node
	// reached is every instance this mount rendered, and visited every scope
	// it rendered one into: what evictUnreached compares.
	reached map[childKey]bool
	visited map[*Env]bool
}

// slotFrame is one component expansion: the instantiation that supplied the
// content, and the scope that content was written in.
//
// The env matters as much as the callsite. Content written at a call site reads
// the caller's vars, not the component's -- `frame { text(value=title) }` means
// the caller's title. Symbols are keyed by declaration, so evaluating it in the
// caller's env is the whole of what lexical scoping needs here.
type slotFrame struct {
	callsite *ir.NodeInst
	env      *Env
	owner    *Env
	// callee is the instance the call site made, and iters, supplied and
	// base are the call site's own, which what it supplies is mounted under.
	callee   *Env
	iters    []string
	supplied *Env
	base     int
}

// iteration is one loop iteration or list-slot insertion: the owner and
// population it was mounted in, and its path among Node.Iters, of which the
// first base were around the population's call site.
type iteration struct {
	owner, supplied *Env
	base            int
	path            []string
	// loop is the *ir.For this is an iteration of, the *ir.SlotContent a
	// list slot inserted, or the *ir.NodeInst whose list rest slot inserted
	// bare children: what a ref matches it to its own loop by.
	loop any
}

// entryFrame is one insertion whose population is being mounted: the content it
// supplied for the slot's component entries, and the scope and slot frames
// that content was written under.
type entryFrame struct {
	ins   *ir.SlotInst
	env   *Env
	slots []slotFrame
}

func (m *mounter) push(f slotFrame) { m.slots = append(m.slots, f) }
func (m *mounter) pop()             { m.slots = m.slots[:len(m.slots)-1] }

func (m *mounter) top() (slotFrame, bool) {
	if len(m.slots) == 0 {
		return slotFrame{}, false
	}
	return m.slots[len(m.slots)-1], true
}

// drop undoes an add for a node the mount decided renders nothing after all.
func (m *mounter) drop(n *Node) {
	delete(m.view.byKey, n.Key)
	if n.ID == "" {
		return
	}
	ids := m.view.byID[n.ID]
	for i, c := range ids {
		if c == n {
			m.view.byID[n.ID] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(m.view.byID[n.ID]) == 0 {
		delete(m.view.byID, n.ID)
	}
}

func (m *mounter) add(n *Node) {
	n.Owner = m.owner
	n.Iters = slices.Clone(m.iters)
	n.SuppliedTo, n.Base = m.supplied, m.base
	if n.Env != nil {
		n.Context = capturedContext(n.Env)
	}
	m.view.byKey[n.Key] = n
	if n.ID != "" {
		m.view.byID[n.ID] = append(m.view.byID[n.ID], n)
	}
}

// bindHandle makes a read of the node's `#id` in the scope it was written in
// the node as it is now mounted: `box.checked` beside the checkbox reads what
// the box shows, which for a two-way prop nobody bound is the instance's cell.
//
// A read written above the node in the same body reads what the previous
// mount bound, since the mount is in order and the node is not there yet.
func (m *mounter) bindHandle(env *Env, inst *ir.NodeInst, n *Node) {
	if inst.Handle != nil && env != nil {
		env.Set(inst.Handle, n.Map())
	}
}

func (m *mounter) key(path string) Key { return Key{Comp: m.root, Path: path} }

// stmts mounts a statement list, returning the nodes it rendered.
func (m *mounter) stmts(env *Env, stmts []ir.Stmt, prefix string) ([]*Node, error) {
	var out []*Node
	nth := map[string]int{}
	next := func(kind string) int {
		n := nth[kind]
		nth[kind] = n + 1
		return n
	}
	join := func(s string) string {
		if prefix == "" {
			return s
		}
		return prefix + "/" + s
	}

	// Visited whether or not it renders a child: one whose only child sat
	// under an `if` that went false still holds that child's state.
	if m.visited != nil {
		m.visited[env] = true
	}

	// A `var` written in the list is state of the block: the list mounts in a
	// scope of its own, kept across mounts by its path as a component's is,
	// and dropped with it when a mount no longer reaches it.
	if hasViewVar(stmts) {
		env = m.blockEnv(env, stmts, join("block"))
	}

	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			nodes, err := m.nodeInst(env, n, join(seg(n.Name, n.ID, next(n.Name))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.CallStmt:
			name, id := elemCallInfo(n)
			if name == "" {
				continue // a method call statement renders nothing
			}
			nodes, err := m.callStmt(env, n, name, id, join(seg(name, id, next(name))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.If:
			p := join(fmt.Sprintf("if@%d", next("if")))
			cond, err := env.Eval(n.Cond)
			b, _ := cond.(bool)
			branch, path := n.Else, p+":else"
			if err == nil && b {
				branch, path = n.Body, p+":then"
			}
			nodes, err := m.stmts(env, branch, path)
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.Match:
			p := join(fmt.Sprintf("match@%d", next("match")))
			i, err := env.matchArm(&n.MatchArms)
			if err != nil || i < 0 {
				continue
			}
			nodes, err := m.stmts(env, n.Arms[i].Body, fmt.Sprintf("%s:arm%d", p, i))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.For:
			nodes, err := m.forStmt(env, n, join(fmt.Sprintf("for@%d", next("for"))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.SlotInst:
			nodes, err := m.slotInst(env, n, join(fmt.Sprintf("slot@%d", next("slot"))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.ErrorBoundary:
			// The `failed` slot stands in place of the content once the
			// boundary has caught. Registered on every render, because the
			// scope has to be able to record a catch before one happens, and
			// idempotently, because a re-render must not forget one.
			body := n.Children
			if len(n.Failed) > 0 {
				env.registerBoundary(n.Handler)
				if env.hasCaught(n.Handler) {
					body = n.Failed
				}
			}
			restore := func() {}
			if n.Handler != nil {
				restore = env.pushRaiseScope(n.Handler)
			}
			nodes, err := m.stmts(env, body, join(fmt.Sprintf("boundary@%d", next("boundary"))))
			restore()
			if raised, ok := err.(*RaisedError); ok && n.Handler != nil {
				// A raise from *mounting* a child, which is the recursion
				// bound and nothing else today: an ordinary error.raise is
				// routed by the ErrorMode the checker resolved, and reaches
				// this boundary as an invoked handler rather than as an error
				// coming back out of the mount. Caught here because a mount
				// has no ir.Call to carry a resolution on, and because the
				// boundary that should catch a nested instance is the dynamic
				// one -- which the mounter is standing in.
				err = env.invokeHandler(n.Handler, raised.Event)
				if IsReturn(err) {
					err = nil
				}
				nodes = nil
			}
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.ContextProvider:
			restore, err := env.pushContext(n.Ref, n.Value)
			if err != nil {
				return nil, err
			}
			nodes, err := m.stmts(env, n.Children, join(fmt.Sprintf("context@%d", next("context"))))
			restore()
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Imperative statements render nothing.

		default:
			return nil, fmt.Errorf("interp.Mount: unhandled ir.Stmt %T", n)
		}
	}
	return out, nil
}

// nodeInst mounts a visual node. A user component with a body is expanded in
// place and contributes no node of its own, which is what both existing walks
// do -- so a component instantiation is never addressable by #id, even when it
// carries one.
func (m *mounter) nodeInst(env *Env, inst *ir.NodeInst, path string) ([]*Node, error) {
	if inst.Component != nil {
		switch inst.Component.Intrinsic {
		case navStackIntrinsic:
			return m.navStack(env, inst, path)
		case navPageIntrinsic:
			return m.navPage(env, inst, path)
		}
	}
	if e, isEffect := effectOf(inst, env, m.key(path)); isEffect {
		m.view.Effects = append(m.view.Effects, e)
		return nil, nil
	}
	if t, isTimer := timerOf(inst, env, m.key(path)); isTimer {
		m.view.Timers = append(m.view.Timers, t)
		return nil, nil
	}
	if inst.Component != nil {
		if env.RenderDepth >= maxRenderDepth {
			return nil, recursionExhausted(inst.Name)
		}
		// The instantiation's own props are the arguments, evaluated in the
		// caller's scope -- the same values componentEnv binds to the
		// component's parameters.
		node := &Node{
			Key:       m.key(path),
			Name:      inst.Name,
			ID:        inst.ID,
			Env:       env,
			Inst:      inst,
			Component: inst.Component,
			Expanded:  len(inst.Component.Stmts) > 0,
		}
		node.Props, node.PropOrder = evalProps(env, inst.Props)
		node.Handlers = handlersOf(inst)
		m.add(node)

		if m.reached != nil {
			m.reached[childKey{inst, path}] = true
			m.visited[env] = true
		}
		child := env.componentEnv(inst.Component, inst, path)
		child.RenderDepth = env.RenderDepth + 1
		child.entries = slices.Clone(m.entries)
		node.CompEnv = child
		if m.byEnv == nil {
			m.byEnv = map[*Env]*Node{}
		}
		m.byEnv[child] = node
		// An unbound two-way prop shows the instance's cell rather than the
		// argument, which only says where the cell started.
		for _, p := range ir.UnboundProps(inst.Component, inst.Bindings) {
			if p.Sym == nil {
				continue
			}
			if v, ok := child.vals[p.Sym]; ok {
				if _, had := node.Props[p.Name]; !had {
					node.PropOrder = append(node.PropOrder, p.Name)
				}
				node.Props[p.Name] = v
			}
		}
		m.bindHandle(env, inst, node)
		if !node.Expanded {
			// Nothing of its own to render, so it stands in the tree as the
			// element does -- children included, which is what the element
			// branch below did for it before it was recognised as a component.
			kids, err := m.stmts(env, inst.Children, path)
			if err != nil {
				return nil, err
			}
			node.Children = kids
			return []*Node{node}, nil
		}
		m.push(slotFrame{callsite: inst, env: env, owner: m.owner, callee: child, iters: m.iters, supplied: m.supplied, base: m.base})
		defer m.pop()
		before := len(m.view.Timers)
		kids, err := m.mountBody(child, path)
		if err != nil {
			return nil, err
		}
		// A component whose whole expansion was schedules draws nothing, so it
		// leaves the tree the way its schedules did. `timer` is that: its
		// override places one primitive and no widget, and a host handed the
		// wrapper got a node with no content whose `interval` prop is a unit
		// value -- which is not a thing a host tree can carry, and the RPC
		// round trip turned it into the unit's whole conversion table.
		if len(kids) == 0 && len(m.view.Timers) > before {
			m.drop(node)
			return nil, nil
		}
		node.Children = kids
		return []*Node{node}, nil
	}

	node := &Node{
		Key:  m.key(path),
		Name: inst.Name,
		ID:   inst.ID,
		Env:  env,
		Inst: inst,
	}
	node.Props, node.PropOrder = evalProps(env, inst.Props)
	node.Handlers = handlersOf(inst)
	m.add(node)
	m.bindHandle(env, inst, node)

	kids, err := m.stmts(env, inst.Children, path)
	if err != nil {
		return nil, err
	}
	node.Children = kids

	// A native element's Children are its real children, mounted above. Its
	// Slots are not read here: only a component declaring insertion points
	// renders supplied content, and it does so at those points -- see slotInst.
	return []*Node{node}, nil
}

func (m *mounter) callStmt(env *Env, cs *ir.CallStmt, name, id, path string) ([]*Node, error) {
	if env.Pkg != nil {
		if comp := FindComponent(env.Pkg, name); comp != nil {
			if env.RenderDepth >= maxRenderDepth {
				return nil, recursionExhausted(name)
			}
			node := &Node{
				Key:       m.key(path),
				Name:      name,
				ID:        id,
				Env:       env,
				Component: comp,
				Props:     map[string]any{},
			}
			if cs.Call != nil {
				for _, arg := range cs.Call.Args {
					if arg.Name == "" {
						continue
					}
					if v, err := env.Eval(arg.Value); err == nil {
						node.Props[arg.Name] = v
						node.PropOrder = append(node.PropOrder, arg.Name)
					}
				}
			}
			m.add(node)

			child := env.ComponentEnvFromCallStmt(comp, cs)
			child.RenderDepth = env.RenderDepth + 1
			node.CompEnv = child
			node.Expanded = len(comp.Stmts) > 0
			if !node.Expanded {
				return []*Node{node}, nil
			}
			// The children-less call form supplies nothing, but it still opens
			// a frame: without one, an insertion point in this component's body
			// would read the *enclosing* call site's content.
			m.push(slotFrame{env: env, owner: m.owner})
			defer m.pop()
			kids, err := m.mountBody(child, path)
			if err != nil {
				return nil, err
			}
			node.Children = kids
			return []*Node{node}, nil
		}
	}

	node := &Node{Key: m.key(path), Name: name, ID: id, Env: env}
	node.Props = map[string]any{}
	if cs.Call != nil {
		for _, arg := range cs.Call.Args {
			if arg.Name == "" {
				continue
			}
			if v, err := env.Eval(arg.Value); err == nil {
				node.Props[arg.Name] = v
				node.PropOrder = append(node.PropOrder, arg.Name)
			}
		}
	}
	// Event handlers are not carried in ir.CallStmt.Args; the AST back-ref is
	// the only place they survive.
	if cstmt, ok := cs.AST.(*ast.CallStmt); ok {
		for _, a := range cstmt.Call.Args.Args {
			if h, ok := a.(ast.EventHandler); ok {
				node.Handlers = append(node.Handlers, Handler{Name: h.Name, Body: &h.Body})
			}
		}
	}
	m.add(node)
	return []*Node{node}, nil
}

// slotInst mounts one insertion point: the content its call site supplied, or
// the insertion's own block as the fallback where it supplied none or what it
// supplied renders nothing -- the rule the splicer states with ir.RendersNothing.
//
// Supplied content is mounted in the *caller's* scope with the caller's frame
// current, because that content is written in the caller's body: a slot inside
// it belongs to the caller's call site, not to this one.
func (m *mounter) slotInst(env *Env, si *ir.SlotInst, path string) ([]*Node, error) {
	if si.Entry != nil {
		return m.entryInst(env, si, path)
	}
	frame, ok := m.top()
	body, sc, supplied := ir.SlotBody(si, frame.callsite)
	if !supplied || !ok {
		return m.stmts(env, body, path)
	}

	cenv := frame.env
	// The *names* in that content are the caller's and the *contexts* are not:
	// a context is set by the provider the content is mounted beneath, and
	// this insertion is where the callee's body puts it. So the caller's
	// scope answers for identifiers and this scope answers for contexts,
	// which is what lets `list { listItem() }` put the item under whatever
	// `list`'s body wrapped its slot in. Swapped and restored rather than
	// snapshotted, because frame.env is the caller's live scope and a copy of
	// it would lose the writes RebindFrom carries back.
	prevCtx := cenv.ContextVals
	cenv.ContextVals = capturedContext(env)
	defer func() { cenv.ContextVals = prevCtx }()
	// A scoped slot's arguments are the insertion's, evaluated here, and the
	// names they bind are the populator's, declared at the call site.
	if sc != nil && len(sc.Params) > 0 {
		cenv = frame.env.Snapshot()
		cenv.mountCopy = true
		for i, prm := range sc.Params {
			if i >= len(si.Args) {
				break
			}
			v, err := env.Eval(si.Args[i])
			if err != nil {
				continue
			}
			cenv.Set(prm, v)
		}
	}

	if sc != nil && len(si.Slots) > 0 {
		m.entries = append(m.entries, entryFrame{ins: si, env: env, slots: slices.Clone(m.slots)})
		defer func() { m.entries = m.entries[:len(m.entries)-1] }()
	}
	m.pop()
	// What a call site supplies is the caller's: bare children are its refs, and
	// a population's nodes are reached through the node it populates, which is
	// the instance they record as SuppliedTo.
	prevOwner, prevIters, prevSupplied, prevBase := m.owner, m.iters, m.supplied, m.base
	m.owner, m.iters, m.supplied, m.base = frame.owner, frame.iters, frame.supplied, frame.base
	// A record's content is written bare at the call site, so it is the
	// caller's, as its other bare children are.
	if sc != nil && (si.Decl == nil || si.Decl.Shows == nil) {
		m.supplied, m.base = frame.callee, len(frame.iters)
		// Each insertion of a list slot is one more copy of the population.
		if slotInserts(frame.callsite, si.Name) == ir.InsertMany {
			m.iters = append(slices.Clip(frame.iters), path)
			m.view.iterations = append(m.view.iterations, iteration{owner: m.owner, supplied: m.supplied, base: m.base, path: m.iters, loop: sc})
		}
	}
	// The path segment marks these nodes as having come from the call site,
	// which is what distinguishes them from an insertion point's fallback.
	nodes, err := m.stmts(cenv, body, path+"/supplied:"+si.Name)
	cenv.writeBackMountCopies()
	m.owner, m.iters, m.supplied, m.base = prevOwner, prevIters, prevSupplied, prevBase
	m.push(frame)
	// Content that renders nothing is the fallback's place as much as no
	// content is: the branch of an `if` that holds none, a forward of a slot
	// its own caller left empty.
	if err != nil || len(nodes) > 0 || len(si.Children) == 0 {
		return nodes, err
	}
	return m.stmts(env, si.Children, path)
}

// entryInst mounts an insertion of a component entry: what the insertion
// whose population this is supplied for the entry, or the entry insertion's
// own block as the fallback. The content was written beside that insertion,
// so it is mounted in that insertion's scope, under the slot frames and entry
// frames that were current there, and with this position's contexts.
func (m *mounter) entryInst(env *Env, x *ir.SlotInst, path string) ([]*Node, error) {
	at := -1
	for i, f := range slices.Backward(m.entries) {
		if f.ins.Decl != nil && slices.Contains(f.ins.Decl.Slots, x.Entry) {
			at = i
			break
		}
	}
	if at < 0 || m.entries[at].ins.Slots[x.Entry.Name] == nil {
		return m.stmts(env, x.Children, path)
	}
	f := m.entries[at]
	content := f.ins.Slots[x.Entry.Name]

	cenv := f.env
	if len(content.Params) > 0 {
		cenv = f.env.Snapshot()
		cenv.mountCopy = true
		for i, prm := range content.Params {
			if i >= len(x.Args) {
				break
			}
			v, err := env.Eval(x.Args[i])
			if err != nil {
				continue
			}
			cenv.Set(prm, v)
		}
	}
	prevCtx := cenv.ContextVals
	cenv.ContextVals = capturedContext(env)
	defer func() { cenv.ContextVals = prevCtx }()

	slots, entries := m.slots, m.entries
	m.slots, m.entries = slices.Clone(f.slots), m.entries[:at]
	defer func() { m.slots, m.entries = slots, entries }()
	owner := m.owner
	m.owner = nil
	defer func() { m.owner = owner }()
	nodes, err := m.stmts(cenv, content.Body, path+"/supplied:"+x.Name)
	cenv.writeBackMountCopies()
	return nodes, err
}

// mountBody mounts a component instance's own body, whose nodes are that
// instance's refs.
func (m *mounter) mountBody(child *Env, path string) ([]*Node, error) {
	owner, iters, supplied, base := m.owner, m.iters, m.supplied, m.base
	m.owner, m.iters, m.supplied, m.base = child, nil, nil, 0
	defer func() { m.owner, m.iters, m.supplied, m.base = owner, iters, supplied, base }()
	return m.stmts(child, child.BodyStmts, path)
}

// forStmt mounts one subtree per iteration.
//
// An iteration is identified by the `key=` of the first top-level node in the
// loop body, evaluated per iteration -- that is the author saying which
// iteration this is, and it is the only identity that survives the list being
// reordered. Without one there is nothing but the index, and a reorder then
// reads as every element's contents changing.
func (m *mounter) forStmt(env *Env, f *ir.For, path string) ([]*Node, error) {
	iterVal, err := env.Eval(f.Iter)
	if err != nil {
		return m.stmts(env, f.Else, path+":else")
	}
	// A list or a computed sequence; asIterable is what keeps a view loop
	// over sngl:seq from materialising one to walk the other.
	n, at, ok := asIterable(iterVal)
	if !ok || n == 0 {
		return m.stmts(env, f.Else, path+":else")
	}
	list, _ := iterVal.([]any)

	keyExpr := loopKeyExpr(f.Body)
	seen := map[string]int{}
	var out []*Node
	iters := m.iters
	defer func() { m.iters = iters }()
	for i := range n {
		child := env.Snapshot()
		child.mountCopy = true
		bindLoopElem(child, f, i, at(i), list)

		id := strconv.Itoa(i)
		if keyExpr != nil {
			if v, evalErr := child.Eval(keyExpr); evalErr == nil {
				id = iterationID(fmt.Sprintf("%v", v), seen)
			}
		}
		copyPath := fmt.Sprintf("%s:body[%s]", path, id)
		m.iters = append(slices.Clip(iters), copyPath)
		m.view.iterations = append(m.view.iterations, iteration{owner: m.owner, supplied: m.supplied, base: m.base, path: m.iters, loop: f})
		nodes, err := m.stmts(child, f.Body, copyPath)
		child.writeBackMountCopies()
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
}

// loopKeyExpr is the `key=` a loop body declares, or nil.
//
// Top-level only: `key=` marks the thing being repeated, and a node nested
// inside the iteration is not that. The checker lifts it off the props into
// NodeInst.Key, so it is never among them.
func loopKeyExpr(body []ir.Stmt) ir.Expr {
	for _, s := range body {
		if n, ok := s.(*ir.NodeInst); ok && n.Key != nil {
			return n.Key
		}
	}
	return nil
}

// iterationID disambiguates a repeated key. Two iterations claiming one
// identity would collide in the key index and silently drop a node, so the
// later ones are suffixed. A duplicate key is the author's bug, but losing a
// node is not the way to report it.
func iterationID(base string, seen map[string]int) string {
	n := seen[base]
	seen[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s~%d", base, n)
}

// handlersOf collects a node's declared events in declaration order.
func handlersOf(inst *ir.NodeInst) []Handler {
	if len(inst.Handlers) == 0 {
		return nil
	}
	out := make([]Handler, 0, len(inst.Handlers))
	for _, h := range inst.Handlers {
		out = append(out, Handler{Name: h.Name, Body: h.Func})
	}
	return out
}

// evalProps evaluates a node's prop assignments, keeping the order they were
// written in. A prop whose expression fails to evaluate is dropped, which is
// what the walks being replaced did: a render must not fail because one
// attribute could not be computed.
func evalProps(env *Env, props []ir.Arg) (map[string]any, []string) {
	out := make(map[string]any, len(props))
	var order []string
	for _, p := range props {
		if p.Name == "" {
			continue
		}
		v, err := env.Eval(p.Value)
		if err != nil {
			continue
		}
		if _, dup := out[p.Name]; !dup {
			order = append(order, p.Name)
		}
		out[p.Name] = v
	}
	return out, order
}

// hasViewVar reports whether a view's statement list declares a `var`.
func hasViewVar(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil {
			return true
		}
	}
	return false
}

// blockEnv is the scope a view's statement list mounts in when it declares a
// `var`: the block's state, as a component's vars are an instance's. It is
// cached by the list's path in the scope it is mounted from, so a re-mount
// keeps what the block's handlers wrote, one per copy under a `for` (each
// iteration's path is its own), and evictUnreached drops it once a mount does
// not reach it -- under an `if` that went false, on a page left -- so the
// block starts again from its initializers.
func (m *mounter) blockEnv(env *Env, stmts []ir.Stmt, at string) *Env {
	if env.childEnvs == nil {
		env.childEnvs = map[childKey]*Env{}
	}
	key := childKey{at: at}
	if m.reached != nil {
		m.reached[key] = true
		m.visited[env] = true
	}
	if cached, ok := env.childEnvs[key]; ok {
		cached.ContextVals = capturedContext(env)
		cached.parent = env
		return cached
	}
	child := NewEnv()
	child.Pkg = env.Pkg
	child.handling = env.handling
	child.wlog = env.writes()
	child.handles = env.owned()
	child.Units = env.Units
	child.Comp = env.Comp
	child.parent = env
	child.inst = env.inst
	child.entries = env.entries
	child.ContextVals = capturedContext(env)
	child.RenderDepth = env.RenderDepth
	for _, s := range stmts {
		if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil {
			child.Set(lv.Sym, evalInit(child, lv.Init))
		}
	}
	env.childEnvs[key] = child
	return child
}

// slotInserts is how often the component a call site instantiates inserts
// its slot name.
func slotInserts(callsite *ir.NodeInst, name string) ir.SlotInserts {
	if callsite == nil || callsite.Component == nil {
		return ir.InsertOne
	}
	for _, s := range callsite.Component.Slots {
		if s.Name == name {
			return s.Inserts
		}
	}
	return ir.InsertOne
}
