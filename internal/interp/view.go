package interp

import (
	"fmt"
	"maps"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// View is the interpreter's retained render tree, mounted once and patched.
type View struct {
	// Roots are the mounted top-level nodes, in source order.
	Roots []*Node
	// byKey indexes every node by its mounted path.
	byKey map[Key]*Node
	// byID indexes by #id. A `for` renders one node per iteration, so an id
	// inside a loop names several.
	byID map[string][]*Node
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
	return len(comp.Body) > 0 || len(comp.Vars) > 0 || len(comp.Funcs) > 0
}

// Mount builds the retained tree for env's component body.
func Mount(env *Env) (*View, error) {
	v := &View{byKey: map[Key]*Node{}, byID: map[string][]*Node{}}
	if env == nil || env.BodyStmts == nil {
		return v, nil
	}
	root := ""
	if env.Comp != nil {
		root = env.Comp.Name
	}
	m := &mounter{view: v, root: root}
	nodes, err := m.stmts(env, env.BodyStmts, "")
	if err != nil {
		return nil, err
	}
	v.Roots = nodes
	return v, nil
}

// Find returns the element nodes carrying the given #id, in mount order.
//
// A component instantiation is skipped even when it carries an id, which is
// what ResolveElementRef has always done: `c.<id>` names an element whose
// props are read, and a component's props are reached through its own fields.
// FindAny is the unfiltered form.
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
	return m
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
}

func (m *mounter) push(f slotFrame) { m.slots = append(m.slots, f) }
func (m *mounter) pop()             { m.slots = m.slots[:len(m.slots)-1] }

func (m *mounter) top() (slotFrame, bool) {
	if len(m.slots) == 0 {
		return slotFrame{}, false
	}
	return m.slots[len(m.slots)-1], true
}

func (m *mounter) add(n *Node) {
	m.view.byKey[n.Key] = n
	if n.ID != "" {
		m.view.byID[n.ID] = append(m.view.byID[n.ID], n)
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
			nodes, err := m.stmts(env, n.Children, join(fmt.Sprintf("boundary@%d", next("boundary"))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.Window:
			nodes, err := m.stmts(env, n.Body, join(fmt.Sprintf("window@%d", next("window"))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.ContextProvider:
			nodes, err := m.stmts(env, n.Children, join(fmt.Sprintf("context@%d", next("context"))))
			if err != nil {
				return nil, err
			}
			out = append(out, nodes...)

		case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
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
		if env.RenderDepth >= maxCallDepth {
			return nil, nil
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
			Expanded:  len(inst.Component.Body) > 0,
		}
		node.Props, node.PropOrder = evalProps(env, inst.Props)
		node.Handlers = handlersOf(inst)
		m.add(node)

		child := env.componentEnv(inst.Component, inst)
		child.RenderDepth = env.RenderDepth + 1
		node.CompEnv = child
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
		m.push(slotFrame{callsite: inst, env: env})
		defer m.pop()
		kids, err := m.stmts(child, child.BodyStmts, path)
		if err != nil {
			return nil, err
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
			if env.RenderDepth >= maxCallDepth {
				return nil, nil
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
			node.Expanded = len(comp.Body) > 0
			if !node.Expanded {
				return []*Node{node}, nil
			}
			// The children-less call form supplies nothing, but it still opens
			// a frame: without one, an insertion point in this component's body
			// would read the *enclosing* call site's content.
			m.push(slotFrame{env: env})
			defer m.pop()
			kids, err := m.stmts(child, child.BodyStmts, path)
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
// the insertion's own block as the fallback. ir.SlotBody makes that choice, so
// the rule is the splicer's and this does not decide it a second time.
//
// Supplied content is mounted in the *caller's* scope with the caller's frame
// current, because that content is written in the caller's body: a slot inside
// it belongs to the caller's call site, not to this one.
func (m *mounter) slotInst(env *Env, si *ir.SlotInst, path string) ([]*Node, error) {
	frame, ok := m.top()
	body, sc, supplied := ir.SlotBody(si, frame.callsite)
	if !supplied || !ok {
		return m.stmts(env, body, path)
	}

	cenv := frame.env
	// A scoped slot's arguments are the insertion's, evaluated here, and the
	// names they bind are the populator's, declared at the call site.
	if sc != nil && len(sc.Params) > 0 {
		cenv = frame.env.Snapshot()
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

	m.pop()
	defer m.push(frame)
	// The path segment marks these nodes as having come from the call site,
	// which is what distinguishes them from an insertion point's fallback.
	return m.stmts(cenv, body, path+"/supplied:"+si.Name)
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
	for i := range n {
		child := env.Snapshot()
		bindLoopElem(child, f, i, at(i), list)

		id := strconv.Itoa(i)
		if keyExpr != nil {
			if v, evalErr := child.Eval(keyExpr); evalErr == nil {
				id = iterationID(fmt.Sprintf("%v", v), seen)
			}
		}
		nodes, err := m.stmts(child, f.Body, fmt.Sprintf("%s:body[%s]", path, id))
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
