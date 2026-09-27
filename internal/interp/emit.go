package interp

import "git.duckfam.us/jonathan/sngl/ir"

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
func (env *Env) runHandler(fn *ir.Func, argEnv *Env, args []ir.CallArg) error {
	for i, p := range fn.Params {
		if i >= len(args) || args[i].Value == nil {
			env.Set(p, nil)
			continue
		}
		v, err := argEnv.Eval(args[i].Value)
		if err != nil {
			return err
		}
		env.Set(p, v)
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
