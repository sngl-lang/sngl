package ir

// ConstraintKind is what con, a type parameter's constraint, asks of a type
// bound to it: the kind of the sngl:builtin constraint it names, BuiltinAny
// for `any` and for none at all.
func ConstraintKind(con *Type) BuiltinKind {
	if con == nil || con.Kind != TypeConstraint {
		return BuiltinAny
	}
	sd, _ := con.Decl.(*StructDef)
	if sd == nil {
		return BuiltinAny
	}
	return sd.Builtin
}

// ConstraintStruct is the anonymous struct con names, `fieldsOf[S]`'s or
// `methodsOf[S]`'s S, nil for a constraint naming none.
func ConstraintStruct(con *Type) *StructDef {
	if con == nil || con.Kind != TypeConstraint || len(con.Elems) == 0 {
		return nil
	}
	return AnonStructDef(con.Elems[0])
}

// ConstraintFielded reports whether con asks for a type with fields:
// `anyStruct`, `anyFields` and `fieldsOf[S]` do.
func ConstraintFielded(con *Type) bool {
	switch ConstraintKind(con) {
	case BuiltinAnyStruct, BuiltinAnyFields, BuiltinFieldsOf:
		return true
	}
	return false
}

// ConstraintFields is the fields con asks a type to have, each at the type it
// asks: `fieldsOf[S]`'s S's, and none for any other.
func ConstraintFields(con *Type) []*StructField {
	if ConstraintKind(con) != BuiltinFieldsOf {
		return nil
	}
	if sd := ConstraintStruct(con); sd != nil {
		return sd.Fields
	}
	return nil
}

// ConstraintMethods is the functions con asks a type to have a method
// fitting, by name: `methodsOf[S]`'s S's, and none for any other.
func ConstraintMethods(con *Type) map[string]*Func {
	if ConstraintKind(con) != BuiltinMethodsOf {
		return nil
	}
	if sd := ConstraintStruct(con); sd != nil {
		return sd.Methods
	}
	return nil
}

// paramConstraint is the constraint on t, a type parameter, nil where t is
// none or nothing constrains it.
func paramConstraint(t *Type) *Type {
	if t == nil || t.Kind != TypeTypeParam {
		return nil
	}
	sym, _ := t.Decl.(*TypeParamSym)
	if sym == nil {
		return nil
	}
	return sym.Constraint
}

// ConstraintMethod is the function named name the constraint on t, a type
// parameter, declares: `[T methodsOf[struct { const func format() string }]]`
// gives a T a format. Nil where t is no type parameter or its constraint names
// none.
func ConstraintMethod(t *Type, name string) *Func {
	return ConstraintMethods(paramConstraint(t))[name]
}

// IsConstraintMethod reports whether fn is a function a constraint declares:
// one with no body, called on a type parameter, which a call names until the
// parameter is bound (BoundCall).
func IsConstraintMethod(fn *Func) bool {
	return fn != nil && fn.Receiver != nil && fn.Receiver.Kind == TypeTypeParam
}

// BoundCall is call as the call it stands for once the types it names are
// bound: a `_format` or `_appendFormat` the bound type's format or
// appendFormat (FormatCall), and a call of a constraint's function the bound
// type's own method of that name. ok is false for any other call, and for one
// whose receiver is not yet bound.
func BoundCall(scope *Scope, call *Call) (Expr, bool) {
	if e, ok := FormatCall(scope, call); ok {
		return e, true
	}
	if call == nil || !IsConstraintMethod(call.Func) || len(call.Args) == 0 {
		return nil, false
	}
	x := call.Args[0].Value
	t := x.ExprType()
	if t == nil || t.HasTypeParam() {
		return nil, false
	}
	fn, params, _, ok := MethodParams(scope, t, call.Func.Name)
	if !ok || IsConstraintMethod(fn) {
		return nil, false
	}
	// Each argument by the name the constraint gives its parameter, which the
	// method declares too, wherever its own list puts it.
	args := []CallArg{{Value: x}}
	named := map[string]bool{}
	for i, a := range call.Args[1:] {
		name := a.Name
		if name == "" && i < len(call.Func.Params) {
			name = call.Func.Params[i].Name
		}
		named[name] = true
		args = append(args, CallArg{Name: name, Value: a.Value})
	}
	// The arguments a format forwards to its elements' formats are written at
	// every call, and the constraint's call wrote none.
	for _, p := range params {
		if !named[p.Name] && IsFormatArgs(p.Type) {
			args = append(args, CallArg{Name: p.Name, Value: FormatArgsLit(scope, p.Type, nil)})
		}
	}
	out := *call
	out.Func = fn
	out.Args = args
	out.ReceiverArg = fn.RecvParam != nil || len(fn.RecvTypeParams) > 0
	return &out, true
}

// FormatArgsLit is the `_formatArgs[X]` t holding every argument X's format
// takes: the field have writes, else the default the parameter declares,
// else -- for the arguments it forwards in turn -- the literal of those.
// have may be nil. A literal written where X was still a type parameter
// holds what the constraint's format takes, and X's may take more.
func FormatArgsLit(scope *Scope, t *Type, have *StructLit) *StructLit {
	out := &StructLit{Type: t}
	if have != nil {
		out.AST, out.Def = have.AST, have.Def
	}
	if !IsFormatArgs(t) {
		return out
	}
	fields, ok := FormatArgsFields(scope, t.Elems[0])
	if !ok {
		return out
	}
	for _, f := range fields {
		var v Expr
		if have != nil {
			for _, w := range have.Fields {
				if w.Name == f.Name {
					v = w.Value
				}
			}
		}
		switch {
		case v != nil:
		case f.Default != nil:
			v = CloneExprSharingDecls(f.Default)
		case IsFormatArgs(f.Type):
			v = FormatArgsLit(scope, f.Type, nil)
		default:
			continue
		}
		out.Fields = append(out.Fields, FieldInit{Name: f.Name, Value: v})
	}
	return out
}

// BoundReach finds the generic functions a call of which BoundCall resolves
// only once a type parameter is bound: each makes one at a type naming its
// own parameters, or calls a generic that does with them, remembering each
// function's answer.
type BoundReach struct {
	memo map[*Func]bool
}

// Reaches reports whether fn, a generic function, makes a call of a
// constraint's function, `_format` or `_appendFormat` at a type naming a type
// parameter, itself or through the generics it calls.
func (r *BoundReach) Reaches(fn *Func) bool {
	if r.memo == nil {
		r.memo = map[*Func]bool{}
	}
	if reaches, ok := r.memo[fn]; ok {
		return reaches
	}
	// A recursion reaches nothing through itself that it does not reach
	// directly.
	r.memo[fn] = false
	reaches := false
	_ = Walk(fn, func(n Node) error {
		if reaches {
			return nil
		}
		if call, ok := n.(*Call); ok && unboundCall(call) {
			reaches = true
			return nil
		}
		callee, b := GenericCallee(n)
		if g, ok := callee.(*Func); ok && g != fn && r.Reaches(g) {
			for _, t := range b {
				if t.HasTypeParam() {
					reaches = true
				}
			}
		}
		return nil
	})
	r.memo[fn] = reaches
	return reaches
}

// unboundCall reports whether call is one BoundCall resolves once the type
// parameter its value's type names is bound.
func unboundCall(call *Call) bool {
	if call.Func == nil || len(call.Args) == 0 {
		return false
	}
	switch {
	case IsConstraintMethod(call.Func), call.Func.Builtin == BuiltinFormatWith, call.Func.Builtin == BuiltinAppendFormatWith:
		return call.Args[0].Value.ExprType().HasTypeParam()
	}
	return false
}
