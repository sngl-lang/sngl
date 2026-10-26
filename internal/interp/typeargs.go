package interp

import "duckfam.us/sngl/ir"

// The interpreter runs a generic body once for every binding, so what a type
// parameter stands for is known only while it runs. Only a codec call asks
// (evalCodecCall): JSON is read and written by the type, not the value.

// typeCall is the binding a call of fn made, waiting for fn's frame to begin.
// The call reads it under the caller's own bindings before its arguments are
// evaluated, and the frame takes it after: a non-pure function runs in the
// caller's scope, where two generics may each name a parameter T.
type typeCall struct {
	fn    *ir.Func
	types map[string]*ir.Type
}

// boundType is t with the type parameters of the running frame substituted.
func (env *Env) boundType(t *ir.Type) *ir.Type {
	if t == nil || len(env.types) == 0 || !t.HasTypeParam() {
		return t
	}
	return t.Substitute(env.types)
}

// resolveTypes is b with every binding read under env's own.
func (env *Env) resolveTypes(b map[string]*ir.Type) map[string]*ir.Type {
	if len(b) == 0 {
		return nil
	}
	out := make(map[string]*ir.Type, len(b))
	for k, t := range b {
		out[k] = env.boundType(t)
	}
	return out
}

// pushCallTypes records what call binds its generic callee's parameters to,
// for runUserFunc to take, and returns what drops it where nothing did.
func (env *Env) pushCallTypes(call *ir.Call) func() {
	callee, b := ir.GenericCallee(call)
	fn, _ := callee.(*ir.Func)
	if fn == nil || env.typeCalls == nil {
		return func() {}
	}
	stack := env.typeCalls
	*stack = append(*stack, typeCall{fn: fn, types: env.resolveTypes(b)})
	n := len(*stack)
	return func() {
		if len(*stack) >= n {
			*stack = (*stack)[:n-1]
		}
	}
}

// takeCallTypes is the binding the innermost pending call of fn made, or nil.
func (env *Env) takeCallTypes(fn *ir.Func) map[string]*ir.Type {
	if env.typeCalls == nil {
		return nil
	}
	stack := *env.typeCalls
	if len(stack) == 0 || stack[len(stack)-1].fn != fn {
		return nil
	}
	top := stack[len(stack)-1].types
	*env.typeCalls = stack[:len(stack)-1]
	return top
}

// componentTypes is what inst binds its generic component's parameters to,
// read under the scope mounting it.
func componentTypes(env *Env, inst *ir.NodeInst) map[string]*ir.Type {
	_, b := ir.GenericCallee(inst)
	return env.resolveTypes(b)
}
