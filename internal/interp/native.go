package interp

import (
	"duckfam.us/sngl/ir"
)

// isCNative reports whether fn is a host's function the interpreter has
// nothing to run for: a C function, whose Block is the checker's `return
// <zero>` and nothing the author wrote, or a `#[go.native]` with no body,
// which a simulated target's override calls (gtk4's `loadPicture`).
func isCNative(fn *ir.Func) bool {
	return fn != nil && (fn.Foreign.Path == "C" || fn.Foreign.Scheme == "go" && fn.Foreign.Name != "")
}

// callCNative stubs a call of a C function. The result is the zero value of
// its type, or a fresh handle where the type is owned, or the argument itself
// for a type's retain; a #[moves] argument is the native's from here, and a
// #[retains] one may not be released until the lifetime making the call ends.
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
	if ir.IsRetain(fn) {
		return env.retainCall(fn, args, func() (any, error) { return args[0], nil })
	}
	h := env.owned()
	for i, p := range fn.Params {
		if hd := handleOf(args[i]); hd != nil {
			switch p.Transfer {
			case ir.TransferMoves:
				h.move(hd)
			case ir.TransferRetains:
				h.retain(hd, env.instance())
			}
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
				var src *Node
				var events map[string]bool
				if ir.IsImageSourceHost(c) {
					src = sourceOf(n)
					events = hostEvents(src)
				}
				for _, h := range n.Handlers {
					fn, _ := h.Body.(*ir.Func)
					if fn == nil || events != nil && !events[h.Name] {
						continue
					}
					payload := func() ([]any, error) { return env.hostPayload(fn.Params, src) }
					if _, _, err := env.runElementHandler(m, h.Name, payload); err != nil {
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

// sourceOf is the primitive an image source host holds: the target's own
// node under the member's override, or nil where the source is empty.
func sourceOf(n *Node) *Node {
	for len(n.Children) > 0 {
		n = n.Children[0]
		if n.Component != nil && n.Component.Primitive() {
			return n
		}
	}
	return nil
}

// hostEvents is the handlers a host calls on a primitive painting an image
// source: `@url` for a URL, `@enter` and `@leave` around a drawing, none for
// an empty source.
func hostEvents(src *Node) map[string]bool {
	if src == nil {
		return map[string]bool{}
	}
	if _, ok := src.Props[ir.ImageSourceURL]; ok {
		return map[string]bool{ir.DrawSourceURL: true}
	}
	return map[string]bool{ir.DrawSourceEnter: true, ir.DrawSourceLeave: true}
}

// hostPayload is what a host hands a paint handler: each parameter its type's
// zero, except that a payload's field named as a prop of the source node
// holds that prop's value, as the target binds it (lower.drawPayloadWith) --
// the URL, and the picture a source holds, which is the source's own value
// and no allocation of the host's.
func (env *Env) hostPayload(params []*ir.Param, src *Node) ([]any, error) {
	out := make([]any, len(params))
	for i, p := range params {
		v, err := env.hostValue(p.Type, src)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (env *Env) hostValue(t *ir.Type, src *Node) (any, error) {
	if t == nil || t.Kind != ir.TypeStruct {
		return zeroValueFor(t), nil
	}
	def, _ := t.Decl.(*ir.StructDef)
	if def == nil || def.Foreign.Name != "" || ir.ReleaseOf(t) != nil {
		if ir.ReleaseOf(t) != nil {
			return nil, nil
		}
		return env.Eval(ir.DeclaredDefault(t))
	}
	s := NewStruct(def, t)
	for _, f := range def.Fields {
		if src != nil {
			if v, ok := src.Props[f.Name]; ok {
				s.Set(f.Name, copyOf(v, f.Type))
				continue
			}
		}
		v, err := env.hostValue(f.Type, src)
		if err != nil {
			return nil, err
		}
		s.Set(f.Name, v)
	}
	return s, nil
}
