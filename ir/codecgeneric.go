package ir

// CodecUse is a codec call a generic declaration makes at a type naming one
// of its own type parameters: what each call binding them is held to, and
// why a target emitting the declaration once with its parameters erased
// copies it per binding instead.
type CodecUse struct {
	Type      *Type
	Format    CodecFormat
	Intrinsic string
}

// CodecReach finds the codec uses of generic declarations, through the
// generics each calls, remembering each declaration's answer.
type CodecReach struct {
	memo map[any][]CodecUse
}

// GenericCodecs is the codec uses of decl, a *Func or a *Component, whose type
// names a type parameter: its own calls of json.decode and the rest, and its
// calls of other generics substituted at what each binds. A use that a call
// binds to a concrete type is that call's to check, and is not decl's.
func (r *CodecReach) GenericCodecs(decl any) []CodecUse {
	if r.memo == nil {
		r.memo = map[any][]CodecUse{}
	}
	if uses, ok := r.memo[decl]; ok {
		return uses
	}
	// A recursion reaches nothing through itself that it does not reach
	// directly.
	r.memo[decl] = nil
	var uses []CodecUse
	add := func(u CodecUse) {
		if !u.Type.HasTypeParam() {
			return
		}
		for _, have := range uses {
			if have.Format == u.Format && have.Intrinsic == u.Intrinsic && have.Type.Equal(u.Type) {
				return
			}
		}
		uses = append(uses, u)
	}
	_ = Walk(decl, func(n Node) error {
		callee, b := GenericCallee(n)
		if call, ok := n.(*Call); ok {
			if t, format, ok := CodecCall(call); ok && t != nil {
				add(CodecUse{Type: t, Format: format, Intrinsic: call.Func.Intrinsic})
			}
		}
		if callee == nil || callee == decl {
			return nil
		}
		for _, u := range r.GenericCodecs(callee) {
			add(CodecUse{Type: u.Type.Substitute(b), Format: u.Format, Intrinsic: u.Intrinsic})
		}
		return nil
	})
	r.memo[decl] = uses
	return uses
}

// GenericCallee is the generic function a call names or the generic component
// a node builds, with what the site binds each of its type parameters to,
// a binding naming the caller's own parameters where the caller is generic
// too; nil for any other node.
func GenericCallee(n Node) (any, map[string]*Type) {
	switch x := n.(type) {
	case *Call:
		fn := x.Func
		if fn == nil || len(fn.TypeParams)+len(fn.RecvTypeParams) == 0 || fn.Intrinsic != "" || len(fn.Block) == 0 {
			return nil, nil
		}
		b := map[string]*Type{}
		for i, p := range fn.Params {
			arg := x.ArgFor(p.Name)
			if arg == nil && i < len(x.Args) && x.Args[i].Name == "" {
				arg = x.Args[i].Value
			}
			if arg != nil {
				BindTypeParams(p.Type, arg.ExprType(), b)
			}
		}
		BindTypeParams(fn.Return, x.Type, b)
		return fn, b
	case *NodeInst:
		c := x.Component
		if c == nil || len(c.TypeParams) == 0 || len(x.TypeArgs) != len(c.TypeParams) {
			return nil, nil
		}
		b := map[string]*Type{}
		for i, tp := range c.TypeParams {
			if t := x.TypeArgs[i]; t != nil {
				b[tp.Name] = t
			}
		}
		return c, b
	}
	return nil, nil
}
