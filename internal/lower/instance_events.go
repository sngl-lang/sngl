package lower

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// passInstanceEvents gives a component that survives inlining a run-time event.
//
// A declared event has only ever worked by substitution: passInlinePure
// replaces the component's `emit(...)` with the block the call site wrote for
// it, so the two bodies become one and nothing about an event is left at run
// time. An instance the build cannot inline -- one under a dynamic `for`, or
// in a recursive cycle -- keeps its emit, and codegen wrote it out as a call
// to `emit`, which nothing declares. The click threw, and the handler the call
// site wrote was in the output nowhere at all.
//
// The fix is to say an event in the vocabulary the instance already has: a
// prop. The event becomes a func-typed prop of the component, the emit becomes
// a call of it, and the handler the call site wrote becomes that prop's value.
// From there the machinery is the one that was already there --
// passComponentProps gives the prop a cell and a `__set_<event>` setter, and
// reuseOrCreate re-points it on every render because componentAbsorbs finds
// that setter.
//
// That last part is the whole reason the event is a prop rather than something
// of its own. A handler written at the call site closes over the iteration:
// `@pick(s) { picked = s }` inside `for var item = items` captures the row it
// was written for. Captured once at construction it would report the row the
// instance was first built for, which is the staleness the retained-node route
// foundered on. A prop cell cannot go stale, because the render writes it
// again every time.
//
// Runs before passComponentProps, which is what turns the prop it adds into
// that cell.
var passInstanceEvents = pass{
	name:    "InstanceEvents",
	enabled: hasInstanceRuntime,
	apply:   lowerInstanceEvents,
}

func lowerInstanceEvents(pkg *ir.Package, _ Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	root := rootComponent(pkg, opts)
	// The components whose events became props, and the parameter each event
	// is called through. Collected first, because an instantiation is
	// rewritten against the declaration and the two are met in no particular
	// order.
	syms := map[*ir.Component]map[string]*ir.Param{}
	for _, c := range pkg.Components {
		if c == nil || c == root || !c.RuntimeInstance || len(c.Events) == 0 {
			continue
		}
		if m := eventProps(c); len(m) > 0 {
			syms[c] = m
		}
	}
	if len(syms) == 0 {
		return nil
	}
	for c, byName := range syms {
		rewriteEmits(c, byName)
	}
	forEachInstanceNode(pkg, func(n *ir.NodeInst) {
		if byName, ok := syms[n.Component]; ok {
			handlersToProps(n, byName)
		}
	})
	return nil
}

// eventProps adds one func-typed prop per declared event and answers with the
// parameter each emit is to be called through.
//
// The default is a no-op of the right shape rather than null: the prop is one
// the call site may leave out, and an event nobody subscribed to has always
// been allowed to fire and go nowhere -- substituteEvents drops such an emit
// on the floor. Calling a lambda that does nothing is that same silence.
//
// The prop is named for the event, except where the event is the write-back
// of a two-way prop: passPropBindings names that event for the prop it reports,
// so the prop holding its handler is `__on_<prop>`. Skipped once that prop
// exists, which keeps running the pass twice a no-op.
func eventProps(c *ir.Component) map[string]*ir.Param {
	byName := make(map[string]*ir.Param, len(c.Events))
	for _, e := range c.Events {
		if e == nil || byName[e.Name] != nil {
			continue
		}
		name := e.Name
		if p := propNamed(c, name); p != nil {
			if !p.Bidirectional {
				continue
			}
			name = "__on_" + e.Name
		}
		if propNamed(c, name) != nil {
			continue
		}
		params := eventParams(e)
		typ := ir.FuncOf(params, nil)
		sym := &ir.Param{Name: name, Type: typ}
		byName[e.Name] = sym
		c.Props = append(c.Props, &ir.Prop{
			Name:    name,
			Type:    typ,
			Default: &ir.Lambda{Type: typ, Func: &ir.Func{Params: params}},
			Sym:     sym,
		})
	}
	return byName
}

// propNamed is c's prop of that name, or nil. An event and a prop cannot share
// one name in source; this is what makes running the pass twice a no-op.
func propNamed(c *ir.Component, name string) *ir.Prop {
	for _, p := range c.Props {
		if p != nil && p.Name == name {
			return p
		}
	}
	return nil
}

// eventParams is the signature an event's handler is called with: the
// event's declared parameters, each given a name the lambda can bind.
func eventParams(e *ir.EventDecl) []*ir.Param {
	if e == nil || len(e.Params) == 0 {
		return nil
	}
	out := make([]*ir.Param, len(e.Params))
	for i, p := range e.Params {
		out[i] = &ir.Param{Name: fmt.Sprintf("__e%d", i), Type: p.Type}
	}
	return out
}

// eventNamed is comp's declaration of that event, or nil.
func eventNamed(comp *ir.Component, name string) *ir.EventDecl {
	if comp == nil {
		return nil
	}
	for _, e := range comp.Events {
		if e != nil && e.Name == name {
			return e
		}
	}
	return nil
}

// padParams and padArgs make both ends of an event agree with the declaration.
//
// An event's arity is loose in source and the substitution that used to
// implement one hid that: a bare `@pick` is declared carrying a `dyn` payload,
// `pick()` may fire it with no argument, and `@pick { … }` may subscribe
// without naming one. bindEventParams simply bound what it was given. A
// func-typed prop is a real signature that a host language type-checks, so the
// declaration is made the one both sides are written to -- the subscription
// gains the parameters it ignored, and the emit passes null for the payload it
// did not send.
func padParams(fn *ir.Func, want []*ir.Param) {
	for i := len(fn.Params); i < len(want); i++ {
		fn.Params = append(fn.Params, &ir.Param{Name: "_", Type: want[i].Type})
	}
}

func padArgs(args []ir.CallArg, want []*ir.Param) []ir.CallArg {
	for i := len(args); i < len(want); i++ {
		args = append(args, ir.CallArg{Value: &ir.Literal{Type: ir.TypNull}})
	}
	return args
}

// rewriteEmits turns every `emit(<event>, args)` in c into a call of the prop
// that event became. An emit naming no declared event is left alone: it is
// already dropped further down, and this pass is not the place to change that.
//
// Every emit c owns, wherever it is written -- a node's handler is where a
// component re-emits, `@click { pick(label) }`. ir.Rewrite stops at
// NodeInst.Component, which is what keeps an emit inside the component being
// instantiated that component's own.
func rewriteEmits(c *ir.Component, byName map[string]*ir.Param) {
	_ = ir.Rewrite(c, func(n ir.Node) (ir.Node, error) {
		emit, ok := n.(*ir.Emit)
		if !ok {
			return n, nil
		}
		sym := byName[emit.Name]
		if sym == nil {
			return n, nil
		}
		return &ir.CallStmt{Call: &ir.Call{
			Type:   ir.TypVoid,
			Callee: &ir.Ident{Name: sym.Name, Type: sym.Type, Sym: sym},
			Args:   padArgs(emit.Args, sym.Type.Sig.Params),
		}}, nil
	})
}

// handlersToProps moves the handlers an instantiation wrote for the
// component's events into its props, as the lambdas they now are.
//
// The handler's own Func is reused rather than copied: it is the block the
// program wrote, and it is about to be the body of the lambda the render hands
// over. Its parameter is whatever name the call site gave the payload, which
// is why the declaration's parameter name is never the one that matters.
func handlersToProps(n *ir.NodeInst, byName map[string]*ir.Param) {
	declared := map[string]bool{}
	for _, e := range n.Component.Events {
		if e != nil {
			declared[e.Name] = true
		}
	}
	kept := n.Handlers[:0]
	for i := range n.Handlers {
		h := n.Handlers[i]
		if h.Func == nil || !declared[h.Name] {
			kept = append(kept, h)
			continue
		}
		want := eventParams(eventNamed(n.Component, h.Name))
		padParams(h.Func, want)
		name := h.Name
		if sym := byName[h.Name]; sym != nil {
			name = sym.Name
		}
		n.Props = append(n.Props, ir.Arg{
			Name:  name,
			Value: &ir.Lambda{Type: ir.FuncOf(want, nil), Func: h.Func},
		})
	}
	n.Handlers = kept
}

// forEachInstanceNode visits every component instantiation in the package,
// wherever a body holds one.
func forEachInstanceNode(pkg *ir.Package, fn func(*ir.NodeInst)) {
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && inst.Component != nil {
			fn(inst)
		}
		return nil
	})
}
