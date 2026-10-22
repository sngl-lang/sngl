package interp

import (
	"duckfam.us/sngl/ir"
)

// cStringDef is the type of the C string a target's Go backend allocates for
// each string argument of a C function (wrapCArgs), named the way it is
// written. Nothing frees one: that is #110.
var cStringDef = &ir.StructDef{Name: "C.CString"}

// isCNative reports whether fn is a C function. Its Block is the checker's
// `return <zero>`, nothing the author wrote.
func isCNative(fn *ir.Func) bool {
	return fn != nil && fn.Foreign.Path == "C"
}

// callCNative stubs a call of a C function. The result is the zero value of
// its type, or a fresh handle where the type is owned; a #[moves] argument is
// the native's from here, and a #[retains] one may not be released until the
// lifetime making the call ends.
func (env *Env) callCNative(call *ir.Call) (any, error) {
	fn := call.Func
	values, err := env.evalCallArgs(call.Args)
	if err != nil {
		return nil, err
	}
	callArgs := call.Args
	if fn.IsMethod() && len(values) == len(fn.Params)+1 {
		callArgs, values = callArgs[1:], values[1:]
	}
	args, err := env.boundArgValues(fn, callArgs, values)
	if err != nil {
		return nil, err
	}
	if ir.IsRelease(fn) {
		return env.releaseCall(fn, args, func() (any, error) { return nil, nil })
	}
	h := env.owned()
	for i, p := range fn.Params {
		if hd := handleOf(args[i]); hd != nil {
			switch p.Transfer {
			case ir.TransferMoves:
				h.move(hd)
			case ir.TransferRetains:
				h.retain(hd)
			}
		}
		if h.simulated != "" && p.Type != nil && p.Type.Kind == ir.TypeString {
			h.alloc(cStringDef, h.site())
		}
	}
	return env.nativeResult(fn.Return)
}

// nativeResult is what a stubbed native answers for its return type t.
func (env *Env) nativeResult(t *ir.Type) (any, error) {
	if t == nil {
		return nil, nil
	}
	if t.Kind != ir.TypeStruct {
		return zeroValueFor(t), nil
	}
	v, err := env.Eval(ir.DeclaredDefault(t))
	if hd := handleOf(v); hd != nil {
		hd.At = env.owned().site()
	}
	return v, err
}

// Paint is one frame of a simulated host: every handler of each mounted
// primitive in the shape family, which is the target calling in to paint
// (Component.Eventless), with each parameter its type's zero. A host may call
// any of them, so a frame calls them all, in tree and declaration order.
// Nothing paints on the interpreter's own.
func (env *Env) Paint(v *View) error {
	if v == nil || env.owned().simulated == "" {
		return nil
	}
	var walk func([]*Node) error
	walk = func(nodes []*Node) error {
		for _, n := range nodes {
			if c := n.Component; c.Primitive() && ir.IsShapeFamily(c.Family) {
				m := n.Map()
				for _, h := range n.Handlers {
					fn, _ := h.Body.(*ir.Func)
					if fn == nil {
						continue
					}
					zeros := func() ([]any, error) { return env.zeros(fn.Params) }
					if _, _, err := env.runElementHandler(m, h.Name, zeros); err != nil {
						return err
					}
				}
			}
			if err := walk(n.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(v.Roots)
}

func (env *Env) zeros(params []*ir.Param) ([]any, error) {
	out := make([]any, len(params))
	for i, p := range params {
		if p.Type == nil || p.Type.Kind != ir.TypeStruct {
			out[i] = zeroValueFor(p.Type)
			continue
		}
		v, err := env.Eval(ir.DeclaredDefault(p.Type))
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
