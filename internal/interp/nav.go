package interp

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// sngl:ui/nav on the interpreter. `sngl:platform/none` overrides `nav.stack`
// with a Stack primitive and `nav.page` with a Page primitive, and these are
// the whole of what the interpreter knows about navigation, recognised by the
// intrinsic ids their declarations carry.
//
// A stack mounts every page it holds, so each page's `#id` is bound to its
// value whether it is showing or not -- `pages.go(about)` names a page that is
// not mounted, and the value is what `current` holds. Each page registers with
// the stack as it mounts, and only the one `current` names mounts its content:
// a page not showing is not mounted, so what it rendered is gone, state
// included, when it is left.
const (
	navStackIntrinsic = "none:Stack"
	navPageIntrinsic  = "none:Page"
)

// navFrame is one stack being mounted: its own scope, which page is current,
// and the pages it holds in the order they mounted.
type navFrame struct {
	env     *Env
	current any
	pages   []*Node
}

// navState is what a stack keeps across mounts, on its instance's scope: the
// entries `go` pushed. An entry is the page and the params it was showing
// with, which is why `back` restores both.
type navState struct {
	history []navEntry
}

type navEntry struct {
	page   any
	params any
}

// navStack mounts a stack. With nothing current yet it mounts once to learn
// its pages, starts at the one whose href is "/" or at its first, and mounts
// again showing it.
func (m *mounter) navStack(env *Env, inst *ir.NodeInst, path string) ([]*Node, error) {
	node := &Node{
		Key:       m.key(path),
		Name:      inst.Name,
		ID:        inst.ID,
		Env:       env,
		Inst:      inst,
		Component: inst.Component,
	}
	node.Props, node.PropOrder = evalProps(env, inst.Props)
	node.Handlers = handlersOf(inst)
	m.add(node)
	m.bindHandle(env, inst, node)

	frame := &navFrame{env: env, current: readProp(env, "current")}
	m.navs = append(m.navs, frame)
	defer func() { m.navs = m.navs[:len(m.navs)-1] }()

	kids, err := m.stmts(env, inst.Children, path)
	if err != nil {
		return nil, err
	}
	if frame.current == nil && len(frame.pages) > 0 {
		start := frame.pages[0]
		for _, p := range frame.pages {
			if href, _ := p.Props["href"].(string); href == "/" {
				start = p
				break
			}
		}
		for _, k := range kids {
			m.dropTree(k)
		}
		frame.current = start.Map()
		frame.pages = nil
		if err := writeProp(env, "current", frame.current); err != nil {
			return nil, err
		}
		if kids, err = m.stmts(env, inst.Children, path); err != nil {
			return nil, err
		}
	}
	node.Children = kids
	return []*Node{node}, nil
}

// navPage mounts a page's primitive, in the scope of the page instance it is
// the override of. It registers the page with the stack being mounted and
// mounts the page's content only when the stack's current is this page.
func (m *mounter) navPage(env *Env, inst *ir.NodeInst, path string) ([]*Node, error) {
	node := &Node{
		Key:       m.key(path),
		Name:      inst.Name,
		ID:        inst.ID,
		Env:       env,
		Inst:      inst,
		Component: inst.Component,
	}
	m.add(node)
	if len(m.navs) == 0 {
		// A page with no stack around it has nothing to be current in.
		return []*Node{node}, nil
	}
	frame := m.navs[len(m.navs)-1]
	if page := m.byEnv[env]; page != nil {
		frame.pages = append(frame.pages, page)
	}
	env.navStack = frame.env
	if instanceEnv(frame.current) != env {
		return []*Node{node}, nil
	}
	kids, err := m.stmts(env, inst.Children, path)
	if err != nil {
		return nil, err
	}
	node.Children = kids
	return []*Node{node}, nil
}

// dropTree takes a node and everything under it out of the view.
func (m *mounter) dropTree(n *Node) {
	for _, k := range n.Children {
		m.dropTree(k)
	}
	m.drop(n)
}

// instanceEnv is the scope of the component instance a handle's value names,
// or nil for a value that is not one. It is the instance's identity: the
// interpreter keeps one scope per instance across mounts.
func instanceEnv(v any) *Env {
	var env *Env
	switch x := v.(type) {
	case map[string]any:
		env, _ = x["__compEnv"].(*Env)
	case InstanceValue:
		env = x.InstanceEnv()
	}
	// A snapshot of an instance's scope is still that instance.
	for env != nil && env.origin != nil {
		env = env.origin
	}
	return env
}

// InstanceValue is a value standing for a component instance by other means
// than the map a handle binds -- the test runner's wrapper, which `c.home`
// reads as -- so that it compares as the instance it wraps.
type InstanceValue interface {
	InstanceEnv() *Env
}

// readProp is an instance's prop as it stands: its cell where the call site
// left a two-way prop unbound, and the call site's value otherwise.
func readProp(env *Env, name string) any {
	if env == nil || env.Comp == nil {
		return nil
	}
	sym := propSym(env.Comp, name)
	if sym == nil {
		return nil
	}
	v, _ := env.Value(sym)
	return v
}

// writeProp writes an instance's two-way prop the way the host reporting a
// change does: through the call site's binding where it wrote one, and into
// the instance's own cell where it did not.
func writeProp(env *Env, name string, v any) error {
	if env == nil || env.Comp == nil {
		return fmt.Errorf("no instance to write %s on", name)
	}
	sym := propSym(env.Comp, name)
	if sym == nil {
		return fmt.Errorf("%s declares no prop %q", env.Comp.Name, name)
	}
	env.Set(sym, v)
	if env.inst == nil || env.parent == nil {
		return nil
	}
	for _, b := range env.inst.Bindings {
		if b.PropName != name {
			continue
		}
		tmp := &ir.Param{Name: "__bound"}
		env.parent.Set(tmp, v)
		err := env.parent.execAssign(&ir.Assign{Target: b.Target, Op: ast.AssignSet, Value: &ir.Ident{Name: tmp.Name, Sym: tmp}})
		delete(env.parent.vals, tmp)
		return err
	}
	return nil
}

// navGo shows `to` in stack, pushing the page it replaces with the params that
// page was showing.
func navGo(stack *Env, to any, params any) error {
	target := instanceEnv(to)
	if stack == nil || target == nil {
		return fmt.Errorf("go: no page to show")
	}
	if stack.nav == nil {
		stack.nav = &navState{}
	}
	if cur := readProp(stack, "current"); instanceEnv(cur) != nil {
		stack.nav.history = append(stack.nav.history, navEntry{page: cur, params: readProp(instanceEnv(cur), "params")})
	}
	if err := writeProp(target, "params", params); err != nil {
		return err
	}
	return writeProp(stack, "current", to)
}

// navBack returns stack to the entry `go` last pushed, params and all.
func navBack(stack *Env) error {
	if stack == nil || stack.nav == nil || len(stack.nav.history) == 0 {
		return nil
	}
	last := stack.nav.history[len(stack.nav.history)-1]
	stack.nav.history = stack.nav.history[:len(stack.nav.history)-1]
	if err := writeProp(instanceEnv(last.page), "params", last.params); err != nil {
		return err
	}
	return writeProp(stack, "current", last.page)
}

func init() {
	intrinsics["nav.go"] = func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("go: want (stack, page, params), got %d args", len(args))
		}
		var params any
		if len(args) > 2 {
			params = args[2]
		}
		return nil, navGo(instanceEnv(args[0]), args[1], params)
	}
	intrinsics["nav.back"] = func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("back: want the stack")
		}
		return nil, navBack(instanceEnv(args[0]))
	}
	// A link names a page and not its stack, so it goes through the stack the
	// page registered with.
	intrinsics["none:follow"] = func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("follow: want (page, params)")
		}
		page := instanceEnv(args[0])
		if page == nil || page.navStack == nil {
			return nil, fmt.Errorf("follow: the page is in no stack")
		}
		var params any
		if len(args) > 1 {
			params = args[1]
		}
		return nil, navGo(page.navStack, args[0], params)
	}
}
