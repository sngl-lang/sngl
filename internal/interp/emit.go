package interp

import "duckfam.us/sngl/ir"

// emit runs the handler the call site supplied for the event a component
// emitted.
//
// This used to be `return nil // no-op in headless tests`, which is invisible
// while every component a fixture places is a leaf the interpreter renders
// itself, and wrong the moment one *is* a composition: a component that
// forwards its own event -- which is what every platform override of a stdlib
// component does -- delivered nothing under `sngl test` while delivering
// correctly on every compiled target, because compiled targets reach the
// handler through passInlinePure's substitution instead.
//
// The handler belongs to the call site, so it runs in the call site's scope:
// the emitting component's env is where `tick()` is written, and the enclosing
// env is where `outer += 1` has to land.
func (env *Env) emit(n *ir.Emit) error {
	if n == nil {
		return nil
	}
	for e := env; e != nil; e = e.parent {
		if e.inst == nil {
			continue
		}
		// The nearest enclosing instantiation is the only one that could have
		// subscribed: an event is declared on one component, and a call site
		// further out named a different declaration's. So this loop finds one
		// frame and then stops, whether or not it subscribed -- an emit with no
		// subscriber goes nowhere, which is what passInlinePure does with it.
		h := handlerNamed(e.inst, n.Name)
		if h == nil || h.Func == nil || e.parent == nil {
			return nil
		}
		return e.parent.runHandler(h.Func, env, n.Args)
	}
	return nil
}

func handlerNamed(inst *ir.NodeInst, name string) *ir.EventHandler {
	for i := range inst.Handlers {
		if inst.Handlers[i].Name == name {
			return &inst.Handlers[i]
		}
	}
	return nil
}

// runHandler executes a handler body in this scope, binding its declared
// parameter to the payload the emit carried.
//
// argEnv is the emitting scope: the payload expression is written there, so it
// is evaluated there and only the resulting value crosses.
//
// The scope may be the copy a mount took for one copy of a loop's body or a
// scoped population, which another handler's writes have made stale since:
// so it reads the scopes it copies first -- after handing them what it wrote
// itself, which a handler still running in it may not have yet -- and hands
// back what the body wrote. Without it two handlers written in one loop each
// read the state the mount saw: `count += 1; done(count)` emitted 1 from both.
// Only through mount copies: a test's subtest runs against a snapshot of its
// parent's state as well, which it must not write back to.
func (env *Env) runHandler(fn *ir.Func, argEnv *Env, args []ir.CallArg) error {
	if env.mountCopy {
		env.writeBackMountCopies()
		for o := env; o.mountCopy && o.origin != nil; o = o.origin {
			env.refreshFrom(o.origin)
		}
		defer env.writeBackMountCopies()
	}
	for i, p := range fn.Params {
		if i >= len(args) || args[i].Value == nil {
			env.Set(p, nil)
			continue
		}
		v, err := argEnv.Eval(args[i].Value)
		if err != nil {
			return err
		}
		env.Set(p, bindArg(v, p))
	}
	// A raise a boundary caught ends the handler that emitted too, as it does on
	// a compiled target, where this body is inlined into that one.
	for _, stmt := range fn.Block {
		err := env.Exec(stmt)
		if ret, ok := err.(*returnSignal); ok && !ret.raised {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// writeBackMountCopies carries what env wrote to each scope it is a mount copy
// of, and no further.
func (env *Env) writeBackMountCopies() {
	for o := env; o.mountCopy && o.origin != nil; o = o.origin {
		o.origin.RebindFrom(env)
	}
}
