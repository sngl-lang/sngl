package interp

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// View is the interpreter's retained render tree.
//
// The interpreter has always rendered by *querying*: ResolveElementRef re-walks
// the component's IR statements, re-evaluating every `if`, `for` and prop
// expression, to find one #id. The snapshot printer walks the same IR a second
// time to produce source. Two walks of one tree that must agree, and neither
// holds anything between calls.
//
// A window cannot work that way. It needs a tree that persists, so a state
// change becomes a patch to what is already on screen rather than a fresh
// render, and so a reload can diff against what is mounted. This is that tree.
//
// It is deliberately the *rendered* tree -- user components are expanded in
// place and contribute no node of their own -- because that is what both
// existing walks produce, and matching them exactly is what lets those walks be
// replaced rather than joined by a third.
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
	// Key is the node's mounted path: the route from the mount root, crossing
	// component instantiations. ComponentKeys is the analogous projection
	// within a single declaration; a mounted node needs the whole chain,
	// because the same declaration mounted twice is two nodes.
	Key Key
	// Name is the resolved element name, ID the #id binding if any.
	Name string
	ID   string
	// Props are the evaluated prop values, and PropOrder the order they were
	// written in -- both the snapshot printer and the emitted platforms depend
	// on that order, so a map alone will not do.
	Props     map[string]any
	PropOrder []string
	// Handlers holds each declared event's body, keyed by event name without
	// the `@`. The value is whatever the IR carried: an *ir.Func for a visual
	// node, an *ast.StmtBlock for the children-less call form.
	Handlers map[string]any
	Children []*Node
	// Env is the scope the props were evaluated in. Held because a handler
	// runs in it, and because a re-evaluation has to use the same scope the
	// mount did.
	Env *Env
	// Inst is the IR this node was mounted from, or nil for the
	// children-less call form. Repointed by a reload; never used as identity.
	Inst *ir.NodeInst
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

// Find returns the nodes carrying the given #id, in mount order.
func (v *View) Find(id string) []*Node {
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
	for k, val := range n.Props {
		m[k] = val
	}
	for ev, h := range n.Handlers {
		m["@"+ev] = h
	}
	m["__ownerEnv"] = n.Env
	return m
}

// mounter carries the mount-wide state: the view being filled and the root
// component's name, which every mounted key is scoped to.
type mounter struct {
	view *View
	root string
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
			nodes, err := m.stmts(env, n.Children, join(fmt.Sprintf("slot@%d", next("slot"))))
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
	if inst.Component != nil && len(inst.Component.Body) > 0 {
		if env.RenderDepth >= maxCallDepth {
			return nil, nil
		}
		child := env.componentEnv(inst.Component, inst)
		child.RenderDepth = env.RenderDepth + 1
		return m.stmts(child, child.BodyStmts, path)
	}

	node := &Node{
		Key:  m.key(path),
		Name: inst.Name,
		ID:   inst.ID,
		Env:  env,
		Inst: inst,
	}
	node.Props, node.PropOrder = evalProps(env, inst.Props)
	if len(inst.Handlers) > 0 {
		node.Handlers = make(map[string]any, len(inst.Handlers))
		for _, h := range inst.Handlers {
			node.Handlers[h.Name] = h.Func
		}
	}
	m.add(node)

	kids, err := m.stmts(env, inst.Children, path)
	if err != nil {
		return nil, err
	}
	node.Children = kids

	// Named slot content is NOT descended into, matching ResolveElementRef,
	// which never looked at NodeInst.Slots. An #id inside a named slot is
	// therefore invisible to both -- a gap to close once the tree is the only
	// walk, so that closing it is one change with one test rather than two
	// walks drifting apart again.
	return []*Node{node}, nil
}

func (m *mounter) callStmt(env *Env, cs *ir.CallStmt, name, id, path string) ([]*Node, error) {
	if env.Pkg != nil {
		if comp := FindComponent(env.Pkg, name); comp != nil {
			if env.RenderDepth >= maxCallDepth {
				return nil, nil
			}
			child := env.ComponentEnvFromCallStmt(comp, cs)
			child.RenderDepth = env.RenderDepth + 1
			return m.stmts(child, child.BodyStmts, path)
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
				if node.Handlers == nil {
					node.Handlers = map[string]any{}
				}
				node.Handlers[h.Name] = &h.Body
			}
		}
	}
	m.add(node)
	return []*Node{node}, nil
}

// forStmt mounts one subtree per iteration.
//
// The iteration is identified by its index. A list node may declare `key=` for
// diffing, and that is the identity a reorder should preserve -- but it sits on
// a node *inside* the body rather than on the loop, so wiring it in is the
// reconciler's job, not the mounter's.
func (m *mounter) forStmt(env *Env, f *ir.For, path string) ([]*Node, error) {
	iterVal, err := env.Eval(f.Iter)
	if err != nil {
		return m.stmts(env, f.Else, path+":else")
	}
	list, ok := iterVal.([]any)
	if !ok || len(list) == 0 {
		return m.stmts(env, f.Else, path+":else")
	}
	var out []*Node
	for i, item := range list {
		child := env.Snapshot()
		child.Set(f.KeySym, item)
		child.Set(f.ValueSym, i)
		nodes, err := m.stmts(child, f.Body, fmt.Sprintf("%s:body[%d]", path, i))
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
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
