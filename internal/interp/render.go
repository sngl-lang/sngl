package interp

import (
	"fmt"
	"maps"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// ResolveElementRef finds visual nodes with the given #id in the current body.
// Returns a single element map, a list of maps (for-loops), or nil.
//
// It mounts a tree and reads it, rather than walking the IR itself. A fresh
// mount per call is what preserves the old behaviour exactly -- every lookup
// re-evaluates against current state -- and the mount becomes a retained one,
// invalidated by a patch, when there is a session loop to own it.
func (env *Env) ResolveElementRef(id string) (any, error) {
	if env.BodyStmts == nil {
		return nil, fmt.Errorf("no visual body for element ref #%s", id)
	}
	v, err := Mount(env)
	if err != nil {
		return nil, err
	}
	nodes := v.Find(id)
	if len(nodes) == 0 {
		return nil, nil
	}
	if len(nodes) == 1 {
		return nodes[0].Map(), nil
	}
	out := make([]any, len(nodes))
	for i, n := range nodes {
		out[i] = n.Map()
	}
	return out, nil
}

// bindProps evaluates a call site's arguments in the caller's scope and binds
// each to the declaration it names, after binding every declared default.
//
// A call site names the prop, so the declaration it means is the component's,
// found by that name. Defaults come first so a prop the call site omitted is
// bound to what the declaration says rather than left unbound: BuildEnv did
// this for the root component and nothing did it for a child, which is
// invisible while an unbound prop only renders as an empty string and wrong
// the moment one is tested.
//
// A two-way prop the call site leaves unbound is the instance's own state
// (ir.UnboundProps), which the host's reports write: its argument is only
// where the cell starts. So it is bound on the first mount, from the argument,
// the default or the type's zero, and skipped on every mount after -- where
// rebinding it would put back what the call site wrote over what the user did.
func bindProps(caller, child *Env, comp *ir.Component, inst *ir.NodeInst, first bool) {
	cells := map[string]bool{}
	for _, p := range ir.UnboundProps(comp, inst.Bindings) {
		cells[p.Name] = true
		if first && p.Default == nil && p.Sym != nil {
			if z := ir.DeclaredDefault(p.Type); z != nil {
				child.Set(p.Sym, evalInit(child, z))
			}
		}
	}
	for _, p := range comp.Props {
		if p.Default == nil || p.Sym == nil || !first && cells[p.Name] {
			continue
		}
		child.Set(p.Sym, evalInit(child, p.Default))
	}
	for _, arg := range inst.Props {
		if arg.Name == "" || !first && cells[arg.Name] {
			continue
		}
		// A declaration whose body was never checked -- a bodyless library
		// component no override answered here -- has props with no symbol,
		// and every one of them would bind the same nil key.
		sym := propSym(comp, arg.Name)
		if sym == nil {
			continue
		}
		v, err := caller.Eval(arg.Value)
		if err == nil {
			child.Set(sym, v)
		}
	}
}

// childKey is one instance: the site, and where in the tree it was mounted.
// A site under a `for` is mounted once per iteration, and each of those holds
// state of its own.
type childKey struct {
	inst *ir.NodeInst
	at   string
}

func (env *Env) componentEnv(comp *ir.Component, inst *ir.NodeInst, at string) *Env {
	if env.childEnvs == nil {
		env.childEnvs = map[childKey]*Env{}
	}
	key := childKey{inst, at}
	if cached, ok := env.childEnvs[key]; ok {
		// The cache is for the component's own state, which has to survive a
		// re-mount. Its props are inputs and must not: they are the call site's
		// expressions, and the call site's state moves. `enabled=running`
		// bound once at first mount left a timer running after a test set
		// `running` to false, because the branch testing it read the value the
		// prop had when the tree was first built.
		bindProps(env, cached, comp, inst, false)
		// And the contexts, for the same reason: they are inputs too, and the
		// provider this instance is mounted under may be answering with a
		// different value than it did last time.
		cached.ContextVals = capturedContext(env)
		// And the scope it answers to. Snapshot() shares the child cache, so a
		// cached child still points at the env it was first mounted from --
		// which for a subtest is the enclosing test's. An emit runs its handler
		// in that scope, so leaving it stale wrote the subtest's timer tick
		// into the test containing it.
		cached.parent = env
		return cached
	}

	child := NewEnv()
	child.Pkg = env.Pkg
	child.handling = env.handling
	child.Units = env.Units
	child.Comp = comp
	child.parent = env
	child.inst = inst
	child.ContextVals = capturedContext(env)

	bindProps(env, child, comp, inst, true)
	for _, v := range comp.Vars {
		child.Set(v, evalInit(child, v.Init))
	}
	child.BodyStmts = comp.Body
	env.childEnvs[key] = child
	return child
}

// ComponentEnv is the public entry point for componentEnv. Used by the
// testrunner to construct live child componentValue wrappers.
func (env *Env) ComponentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	return env.componentEnv(comp, inst, "")
}

// ComponentEnvFromCallStmt returns (and caches) the child env for a user
// component instantiated as a children-less CallStmt (`comp()`). Keyed by
// the *ir.CallStmt so state survives across re-renders.
func (env *Env) ComponentEnvFromCallStmt(comp *ir.Component, cs *ir.CallStmt) *Env {
	if env.callChildEnvs == nil {
		env.callChildEnvs = map[*ir.CallStmt]*Env{}
	}
	if cached, ok := env.callChildEnvs[cs]; ok {
		return cached
	}
	child := env.componentEnvFromCall(comp, cs.Call)
	env.callChildEnvs[cs] = child
	return child
}

// RenderCallStmtNode renders a children-less element call (text #id(...))
// as an element map. Returns nil for non-element CallStmts (e.g. a method
// call statement).
func (env *Env) RenderCallStmtNode(cs *ir.CallStmt) map[string]any {
	elemName, _ := elemCallInfo(cs)
	if elemName == "" {
		return nil
	}
	return env.renderCallStmtProps(cs, elemName)
}

// elemCallInfo extracts (name, id) from an element CallStmt's AST back-ref.
func elemCallInfo(cs *ir.CallStmt) (string, string) {
	if cs == nil || cs.Call == nil {
		return "", ""
	}
	name := ""
	id := ""
	if call := cs.Call.AST; call != nil {
		id = call.ID
		switch f := call.Func.(type) {
		case *ast.SelectExpr:
			if ident, ok := f.Operand.(*ast.IdentExpr); ok {
				name = ident.Name
			}
		case *ast.IdentExpr:
			name = f.Name
		}
	}
	return name, id
}

func (env *Env) renderCallStmtProps(cs *ir.CallStmt, elemName string) map[string]any {
	m := map[string]any{"_type": elemName}
	if cs.Call == nil {
		return m
	}
	for _, arg := range cs.Call.Args {
		if arg.Name == "" {
			continue
		}
		if v, err := env.Eval(arg.Value); err == nil {
			m[arg.Name] = v
		}
	}
	// Event handlers aren't captured in ir.CallStmt.Args — reach into the
	// AST back-ref to collect their bodies for later invocation via @event().
	if cstmt, ok := cs.AST.(*ast.CallStmt); ok {
		for _, a := range cstmt.Call.Args.Args {
			if h, ok := a.(ast.EventHandler); ok {
				m["@"+h.Name] = &h.Body
			}
		}
	}
	m["__ownerEnv"] = env
	m["__ownerContext"] = capturedContext(env)
	return m
}

// componentEnvFromCall creates a child env for expanding a user component
// invoked as a CallStmt (no children block).
func (env *Env) componentEnvFromCall(comp *ir.Component, call *ir.Call) *Env {
	child := NewEnv()
	child.Pkg = env.Pkg
	child.handling = env.handling
	child.Units = env.Units
	child.Comp = comp
	child.parent = env
	child.ContextVals = capturedContext(env)

	// Override with named positional args from the call.
	if call != nil {
		for _, a := range call.Args {
			if a.Name == "" {
				continue
			}
			if v, err := env.Eval(a.Value); err == nil {
				child.Set(propSym(comp, a.Name), v)
			}
		}
	}
	for _, v := range comp.Vars {
		child.Set(v, evalInit(child, v.Init))
	}
	child.BodyStmts = comp.Body
	return child
}

// RenderNodeProps is the public entry point for renderNodeProps. Returns
// a map of prop-name → evaluated value for a native element. Used by the
// testrunner to expose native elements as dicts in c.children.
func (env *Env) RenderNodeProps(node *ir.NodeInst) map[string]any {
	return env.renderNodeProps(node)
}

func (env *Env) renderNodeProps(node *ir.NodeInst) map[string]any {
	m := map[string]any{"_type": node.Name}
	for _, p := range node.Props {
		if p.Name == "" {
			continue
		}
		if v, err := env.Eval(p.Value); err == nil {
			m[p.Name] = v
		}
	}
	for _, h := range node.Handlers {
		m["@"+h.Name] = h.Func
	}
	m["__ownerEnv"] = env
	m["__ownerContext"] = capturedContext(env)
	m["__inst"] = node
	return m
}

// capturedContext is the context values an instance is mounted under, copied
// rather than shared.
//
// A context is *dynamic*: its value is the one the provider a node is mounted
// beneath is answering with, not one the scope holding the node's names has.
// The mounter tracks that by pushing onto the enclosing env's map and
// restoring on the way out, so during the mount `env.ContextVals` is exactly
// the right answer -- and after it, it is exactly the wrong one. A test
// reading `c.leaf.label.value` evaluates the prop then, long after every
// provider has been unwound, so an instance that shared the map read the
// default. Copying at mount is what makes the value outlive the descent.
//
// Left out entirely, a provider set nothing beyond the body it was written in:
// `theme("dark") { leaf() }` read the default inside leaf, on every target
// with no host language.
func capturedContext(env *Env) map[*ir.Context]any {
	if len(env.ContextVals) == 0 {
		return nil
	}
	return maps.Clone(env.ContextVals)
}
