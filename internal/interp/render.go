package interp

import (
	"fmt"
	"maps"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
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
func bindProps(caller, child *Env, comp *ir.Component, inst *ir.NodeInst) {
	for _, p := range comp.Props {
		if p.Default == nil {
			continue
		}
		child.Set(p.Sym, evalInit(child, p.Default))
	}
	for _, arg := range inst.Props {
		if arg.Name == "" {
			continue
		}
		v, err := caller.Eval(arg.Value)
		if err == nil {
			child.Set(propSym(comp, arg.Name), v)
		}
	}
}

func (env *Env) componentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	if env.childEnvs == nil {
		env.childEnvs = map[*ir.NodeInst]*Env{}
	}
	if cached, ok := env.childEnvs[inst]; ok {
		// The cache is for the component's own state, which has to survive a
		// re-mount. Its props are inputs and must not: they are the call site's
		// expressions, and the call site's state moves. `enabled=running`
		// bound once at first mount left a timer running after a test set
		// `running` to false, because the branch testing it read the value the
		// prop had when the tree was first built.
		bindProps(env, cached, comp, inst)
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
	child.Units = env.Units
	child.Comp = comp
	child.parent = env
	child.inst = inst
	child.ContextVals = capturedContext(env)

	bindProps(env, child, comp, inst)
	for _, v := range comp.Vars {
		child.Set(v, evalInit(child, v.Init))
	}
	child.BodyStmts = comp.Body
	env.childEnvs[inst] = child
	return child
}

// ComponentEnv is the public entry point for componentEnv. Used by the
// testrunner to construct live child componentValue wrappers.
func (env *Env) ComponentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	return env.componentEnv(comp, inst)
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
	return m
}

// componentEnvFromCall creates a child env for expanding a user component
// invoked as a CallStmt (no children block).
func (env *Env) componentEnvFromCall(comp *ir.Component, call *ir.Call) *Env {
	child := NewEnv()
	child.Pkg = env.Pkg
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
