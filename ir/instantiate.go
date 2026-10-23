package ir

import "reflect"

// InstantiateFunc copies fn with its type parameters bound: every type in the
// copy naming one of them names its binding instead. What fn declares -- its
// parameters, its locals and loop variables, and the lambdas in its body with
// theirs -- is copied, and everything it refers to beyond that is shared, so a
// call in the copy still resolves to the declaration the original's did. That
// includes a call of fn itself, which is bound by its own arguments: under
// polymorphic recursion it is not the copy making it.
func InstantiateFunc(fn *Func, bindings map[string]*Type) *Func {
	if fn == nil {
		return nil
	}
	c := newCloner()
	c.shareDecls = true
	c.subst = bindings
	c.own = ownDecls(fn)
	c.deepNext = true
	out := c.clone(reflect.ValueOf(fn)).Interface().(*Func)
	_ = Walk(out, func(n Node) error {
		switch x := n.(type) {
		case *Call:
			if x.Func == out {
				x.Func = fn
			}
		case *Ident:
			if x.Sym == Symbol(out) {
				x.Sym = fn
			}
		}
		return nil
	})
	out.TypeParams = nil
	out.RecvTypeParams = nil
	return out
}

// InstantiateStmts copies stmts with bindings applied to every type in them,
// as InstantiateFunc copies a function's body: what the statements declare is
// copied, and everything they refer to beyond that is shared. It is how a
// generic component's body is spliced where the call site bound its type
// parameters -- left as written, `struct { v T }` in it reached a backend that
// erases T.
func InstantiateStmts(stmts []Stmt, bindings map[string]*Type) []Stmt {
	if len(stmts) == 0 || len(bindings) == 0 {
		return stmts
	}
	c := newCloner()
	c.shareDecls = true
	c.subst = bindings
	c.own = map[uintptr]bool{}
	addOwnDecls(c.own, stmts)
	return c.clone(reflect.ValueOf(stmts)).Interface().([]Stmt)
}

// InstantiateExpr is e copied with bindings applied to every type in it, as
// InstantiateStmts copies a statement list: a prop's default written in a
// generic declaration, read at the types a call site bound.
func InstantiateExpr(e Expr, bindings map[string]*Type) Expr {
	if e == nil || len(bindings) == 0 {
		return e
	}
	c := newCloner()
	c.shareDecls = true
	c.subst = bindings
	c.own = map[uintptr]bool{}
	return c.clone(reflect.ValueOf(&e).Elem()).Interface().(Expr)
}

// InstantiateComponent copies c with its type parameters bound, as
// InstantiateFunc copies a function: what c declares -- its props, events and
// slots with their parameters, its state, its methods with what they declare,
// the handles its body names and everything its body declares -- is copied,
// and what it refers to beyond that is shared. A node in the copy rendering c
// itself names c again, being bound by its own type arguments: under
// polymorphic recursion it is not the copy it sits in.
func InstantiateComponent(c *Component, bindings map[string]*Type) *Component {
	if c == nil {
		return nil
	}
	cl := newCloner()
	cl.shareDecls = true
	cl.subst = bindings
	cl.own = ownComponentDecls(c)
	cl.deepNext = true
	out := cl.clone(reflect.ValueOf(c)).Interface().(*Component)
	_ = Walk(out, func(n Node) error {
		if x, ok := n.(*NodeInst); ok && x.Component == out {
			x.Component = c
		}
		return nil
	})
	out.TypeParams = nil
	out.PlatformOverrides, out.LanguageOverrides = nil, nil
	return out
}

// ownComponentDecls is the set of declarations c's own text declares.
func ownComponentDecls(c *Component) map[uintptr]bool {
	own := map[uintptr]bool{reflect.ValueOf(c).Pointer(): true}
	add := func(x any) { own[reflect.ValueOf(x).Pointer()] = true }
	params := func(ps []*Param) {
		for _, p := range ps {
			if p != nil {
				add(p)
			}
		}
	}
	for _, p := range c.Props {
		if p != nil && p.Sym != nil {
			add(p.Sym)
		}
	}
	for _, e := range c.Events {
		if e != nil {
			params(e.Params)
		}
	}
	var slots func([]*SlotDecl)
	slots = func(ss []*SlotDecl) {
		for _, s := range ss {
			if s != nil {
				add(s)
				params(s.Params)
				slots(s.Slots)
			}
		}
	}
	slots(c.Slots)
	for _, v := range c.Vars {
		if v != nil {
			add(v)
		}
	}
	for _, f := range c.Funcs {
		if f != nil {
			for k := range ownDecls(f) {
				own[k] = true
			}
		}
	}
	addOwnDecls(own, c.Stmts)
	_ = Walk(c.Stmts, func(n Node) error {
		if x, ok := n.(*NodeInst); ok && x.Handle != nil {
			add(x.Handle)
		}
		return nil
	})
	return own
}

// ComponentBindings is what the call site of n bound its component's type
// parameters to, by name; nil for a component that declares none or a node
// that records no binding for one.
func ComponentBindings(n *NodeInst) map[string]*Type {
	if n == nil || n.Component == nil || len(n.Component.TypeParams) == 0 || len(n.TypeArgs) != len(n.Component.TypeParams) {
		return nil
	}
	b := make(map[string]*Type, len(n.TypeArgs))
	for i, tp := range n.Component.TypeParams {
		if t := n.TypeArgs[i]; t != nil && !t.HasTypeParam() {
			b[tp.Name] = t
		}
	}
	if len(b) == 0 {
		return nil
	}
	return b
}

// ownDecls is the set of declarations fn's own text declares.
func ownDecls(fn *Func) map[uintptr]bool {
	own := map[uintptr]bool{reflect.ValueOf(fn).Pointer(): true}
	for _, p := range fn.Params {
		own[reflect.ValueOf(p).Pointer()] = true
	}
	if fn.RecvParam != nil {
		own[reflect.ValueOf(fn.RecvParam).Pointer()] = true
	}
	addOwnDecls(own, fn.Block)
	return own
}

// addOwnDecls adds to own what stmts declare: their locals and loop variables,
// and the lambdas and node handlers in them with their parameters.
func addOwnDecls(own map[uintptr]bool, stmts []Stmt) {
	addFunc := func(f *Func) {
		own[reflect.ValueOf(f).Pointer()] = true
		for _, p := range f.Params {
			own[reflect.ValueOf(p).Pointer()] = true
		}
		if f.RecvParam != nil {
			own[reflect.ValueOf(f.RecvParam).Pointer()] = true
		}
	}
	_ = Walk(stmts, func(n Node) error {
		switch x := n.(type) {
		case *LocalVar:
			if x.Sym != nil {
				own[reflect.ValueOf(x.Sym).Pointer()] = true
			}
		case *For:
			for _, v := range []*LoopVar{x.KeySym, x.ValueSym} {
				if v != nil {
					own[reflect.ValueOf(v).Pointer()] = true
				}
			}
		case *Lambda:
			if x.Func != nil {
				addFunc(x.Func)
			}
		case *Closure:
			if x.Func != nil {
				addFunc(x.Func)
			}
		case *NodeInst:
			for _, h := range x.Handlers {
				if h.Func != nil {
					addFunc(h.Func)
				}
			}
		}
		return nil
	})
}

// BindTypeParams binds the type parameters pattern names from the type arg
// has in the same position, leaving a parameter already bound as it is:
// `list[T]` against `list[int]` binds T to int, `func(T) U` against
// `func(int) string` both, and an anonymous struct binds by field name from
// whatever fields arg has -- `{v=1}`, a named struct with a v, a type
// parameter constrained to one. A binding may itself name a type parameter
// (the checker binds a callee's T to a caller's); one that wants concrete
// types only drops those.
func BindTypeParams(pattern, arg *Type, bindings map[string]*Type) {
	if pattern == nil || arg == nil {
		return
	}
	if pattern.Kind == TypeTypeParam {
		if _, ok := bindings[pattern.ParamName]; !ok {
			bindings[pattern.ParamName] = arg
		}
		return
	}
	if sd := AnonStructDef(pattern); sd != nil {
		if fields, ok := FieldsOf(arg); ok {
			for _, f := range sd.Fields {
				if g := FieldNamed(fields, f.Name); g != nil {
					BindTypeParams(f.Type, g.Type, bindings)
				}
			}
		}
		return
	}
	if pattern.Kind != arg.Kind {
		return
	}
	if pattern.Kind == TypeFunc {
		if pattern.Sig != nil && arg.Sig != nil {
			if len(pattern.Sig.Params) == len(arg.Sig.Params) {
				for i := range pattern.Sig.Params {
					BindTypeParams(pattern.Sig.Params[i].Type, arg.Sig.Params[i].Type, bindings)
				}
			}
			BindTypeParams(pattern.Sig.Return, arg.Sig.Return, bindings)
		}
		return
	}
	if len(pattern.Elems) == len(arg.Elems) {
		for i := range pattern.Elems {
			BindTypeParams(pattern.Elems[i], arg.Elems[i], bindings)
		}
	}
}

// HasTypeParam reports whether t names a type parameter anywhere in it.
func (t *Type) HasTypeParam() bool {
	return t.Any(func(c *Type) bool { return c.Kind == TypeTypeParam })
}

// SubstituteExprTypes copies e with every type in it bound under bindings,
// sharing the declarations it names.
func SubstituteExprTypes(e Expr, bindings map[string]*Type) Expr {
	if e == nil {
		return nil
	}
	c := newCloner()
	c.shareDecls = true
	c.subst = bindings
	return c.clone(reflect.ValueOf(e)).Interface().(Expr)
}
