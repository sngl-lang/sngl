package interp

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// sngl:ui/nav on the interpreter. `sngl:platform/none` overrides `nav.stack`
// with a Stack primitive and `nav.page` with a Page primitive, and these are
// the whole of what the interpreter knows about navigation, recognised by the
// intrinsic ids their declarations carry.
//
// A stack mounts every page it holds, so each page's `#id` is bound to its
// value whether it is showing or not -- `pages.go(about)` names a page that is
// not mounted, and the value is what `current` answers. Each page registers
// with the stack as it mounts, and only the one showing mounts its content: a
// page not showing is not mounted, so what it rendered is gone, state
// included, when it is left.
//
// Which page shows is the stack's state and the params a page shows with are
// the page's, both kept here rather than in a prop: a stack declares no
// `current` and a page's `params` is only where it starts.
const (
	navStackIntrinsic = "none:Stack"
	navPageIntrinsic  = "none:Page"
)

// navFrame is one stack being mounted: its own scope and the pages it holds
// in the order they mounted.
type navFrame struct {
	env   *Env
	pages []*Node
}

// navState is what a stack keeps across mounts, on its instance's scope: the
// page showing, and the entries `go` pushed. An entry is the page and the
// params it was showing with, which is why `back` restores both.
type navState struct {
	current any
	history []navEntry
}

type navEntry struct {
	page   any
	params *navParams
}

// navParams is the params a page is showing with, when something handed it
// some; a page with none shows its own `params` prop.
type navParams struct {
	v any
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

	if env.nav == nil {
		env.nav = &navState{}
	}
	frame := &navFrame{env: env}
	m.navs = append(m.navs, frame)
	defer func() { m.navs = m.navs[:len(m.navs)-1] }()

	kids, err := m.stmts(env, inst.Children, path)
	if err != nil {
		return nil, err
	}
	if env.nav.current == nil && len(frame.pages) > 0 {
		start := frame.pages[0]
		for _, p := range frame.pages {
			if href, _ := p.Props[ir.NavPageHref].(string); href == "/" {
				start = p
				break
			}
		}
		for _, k := range kids {
			m.dropTree(k)
		}
		env.nav.current = start.Map()
		frame.pages = nil
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
	if instanceEnv(frame.env.nav.current) != env {
		return []*Node{node}, nil
	}
	// The params it was handed, over the prop the call site wrote, for as long
	// as the content mounts: the population reads them there, and the prop
	// itself stays what the call site wrote, which `pkg.params` reads.
	if sym := propSym(env.Comp, ir.NavPageParams); sym != nil && env.navParams != nil {
		written, had := env.vals[sym]
		env.Set(sym, env.navParams.v)
		defer func() {
			if had {
				env.vals[sym] = written
			} else {
				delete(env.vals, sym)
			}
		}()
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

// navGo shows `to` in stack, pushing the page it replaces with the params that
// page was showing. Params of nil are none handed, and the page shows its own.
func navGo(stack *Env, to any, params any) error {
	target := instanceEnv(to)
	if stack == nil || target == nil {
		return fmt.Errorf("go: no page to show")
	}
	if stack.nav == nil {
		stack.nav = &navState{}
	}
	if cur := instanceEnv(stack.nav.current); cur != nil {
		stack.nav.history = append(stack.nav.history, navEntry{page: stack.nav.current, params: cur.navParams})
	}
	target.navParams = nil
	if params != nil {
		target.navParams = &navParams{v: params}
	}
	stack.nav.current = to
	return nil
}

// navBack returns stack to the entry `go` last pushed, params and all.
func navBack(stack *Env) error {
	if stack == nil || stack.nav == nil || len(stack.nav.history) == 0 {
		return nil
	}
	last := stack.nav.history[len(stack.nav.history)-1]
	stack.nav.history = stack.nav.history[:len(stack.nav.history)-1]
	if page := instanceEnv(last.page); page != nil {
		page.navParams = last.params
	}
	stack.nav.current = last.page
	return nil
}

func init() {
	intrinsics[ir.NavGoID] = func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("go: want (stack, page, params), got %d args", len(args))
		}
		var params any
		if len(args) > 2 {
			params = args[2]
		}
		return nil, navGo(instanceEnv(args[0]), args[1], params)
	}
	intrinsics[ir.NavCurrentID] = func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("current: want the stack")
		}
		stack := instanceEnv(args[0])
		if stack == nil || stack.nav == nil {
			return nil, nil
		}
		return stack.nav.current, nil
	}
	intrinsics[ir.NavBackID] = func(args []any) (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("back: want the stack")
		}
		return nil, navBack(instanceEnv(args[0]))
	}
	// A link names a page and not its stack, so it goes through the stack the
	// page registered with.
	intrinsics[ir.NavFollowID] = func(args []any) (any, error) {
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
