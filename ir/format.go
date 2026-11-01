package ir

import "duckfam.us/sngl/ast"

// ResolveMethod is the method named field a value of type receiver has, as a
// lookup through scope sees it, and the type it is a method of: the
// declaration's own member, else one by the type's name, else a builtin
// generic's by its constructor's (`list[int]` asks `list`), else, through an
// `option` of a struct, the struct's. A type parameter's is its constraint's
// (ConstraintMethod).
func ResolveMethod(scope *Scope, receiver *Type, field string) (*Func, *Type, bool) {
	if receiver == nil {
		return nil, receiver, false
	}
	if fn := ConstraintMethod(receiver, field); fn != nil {
		return fn, receiver, true
	}
	if sym, ok := receiver.Decl.(Symbol); ok {
		if fn, ok := MethodOn(scope, sym, field); ok {
			return fn, receiver, true
		}
	}
	if fn, ok := LookupMethodIn(scope, receiver.String(), field); ok {
		return fn, receiver, true
	}
	generic := ""
	switch receiver.Kind {
	case TypeList:
		generic = "list"
	case TypeOption:
		generic = "option"
	case TypeMap:
		generic = "map"
	}
	if generic != "" {
		if fn, ok := LookupMethodIn(scope, generic, field); ok {
			return fn, receiver, true
		}
	}
	if field == "format" {
		if fn := DefaultFormat(receiver); fn != nil {
			return fn, receiver, true
		}
	}
	// Asked after `option`'s own members, so a name the option itself declares
	// still wins.
	if receiver.Kind == TypeOption && len(receiver.Elems) == 1 &&
		receiver.Elems[0] != nil && receiver.Elems[0].Kind == TypeStruct {
		if sym, ok := receiver.Elems[0].Decl.(Symbol); ok {
			if fn, ok := MethodOn(scope, sym, field); ok {
				return fn, receiver.Elems[0], true
			}
		}
	}
	return nil, receiver, false
}

// IsFormatArgs reports whether t is a `_formatArgs[X]`, the arguments X's
// format takes after its receiver.
func IsFormatArgs(t *Type) bool {
	if t == nil || t.Kind != TypeStruct || len(t.Elems) != 1 {
		return false
	}
	sd, ok := t.Decl.(*StructDef)
	return ok && sd.Builtin == BuiltinFormatArgs
}

// FormatArgsParam is the index of fn's `_formatArgs` parameter, which takes
// the named arguments a call gives that fn declares no parameter for, or -1.
func FormatArgsParam(params []*Param) int {
	for i, p := range params {
		if IsFormatArgs(p.Type) {
			return i
		}
	}
	return -1
}

// FormatParams is x's method named method, format or appendFormat, and the
// parameters a call of it is given by name: those after its receiver -- and
// after the builder an appendFormat appends to -- at the types x binds. ok is
// false where x declares no such method.
func FormatParams(scope *Scope, x *Type, method string) (fn *Func, params []*Param, ok bool) {
	fn, params, _, ok = MethodParams(scope, x, method)
	if !ok {
		return nil, nil, false
	}
	if method == "appendFormat" {
		if len(params) == 0 {
			return nil, nil, false
		}
		params = params[1:]
	}
	return fn, params, true
}

// MethodParams is x's method named method, the parameters a call of it is
// given after its receiver and its result, each at the types x binds. ok is
// false where x declares no such method.
func MethodParams(scope *Scope, x *Type, method string) (fn *Func, params []*Param, result *Type, ok bool) {
	fn, recv, ok := ResolveMethod(scope, x, method)
	if !ok || fn == nil {
		return nil, nil, nil, false
	}
	params = fn.Params
	b := map[string]*Type{}
	if len(fn.RecvTypeParams) > 0 && len(recv.Elems) == len(fn.RecvTypeParams) {
		for i, tp := range fn.RecvTypeParams {
			b[tp.Name] = recv.Elems[i]
		}
	}
	if fn.RecvParam == nil && len(fn.RecvTypeParams) == 0 && takesReceiver(fn) {
		BindTypeParams(params[0].Type, recv, b)
		params = params[1:]
	}
	out := make([]*Param, len(params))
	for i, p := range params {
		q := *p
		if len(b) > 0 {
			q.Type = p.Type.Substitute(b)
		}
		out[i] = &q
	}
	return fn, out, fn.Return.Substitute(b), true
}

// FormatArgDecl is the parameter a call's argument named name reaches through
// the `_formatArgs` of type t it is taken by, and the format declaring it: a
// parameter of X's format, or, through a `_formatArgs` that format declares in
// turn, one further in. Nil where there is none.
func FormatArgDecl(scope *Scope, t *Type, name string) (*Func, *Param) {
	for IsFormatArgs(t) {
		fn, params, ok := FormatParams(scope, t.Elems[0], "format")
		if !ok {
			return nil, nil
		}
		t = nil
		for _, p := range params {
			if p.Name == name {
				for _, q := range fn.Params {
					if q.Name == name {
						return fn, q
					}
				}
				return nil, nil
			}
			if IsFormatArgs(p.Type) {
				t = p.Type
			}
		}
	}
	return nil, nil
}

// FormatArgsFields is the fields of `_formatArgs[x]`: one per parameter of
// x's format after its receiver, at the types x binds, with the default the
// parameter declares. ok is false where x declares no format.
func FormatArgsFields(scope *Scope, x *Type) ([]*StructField, bool) {
	_, params, ok := FormatParams(scope, x, "format")
	if !ok {
		return nil, false
	}
	fields := make([]*StructField, len(params))
	for i, p := range params {
		fields[i] = &StructField{Name: p.Name, Type: p.Type, Default: p.Default, DefaultWritten: p.Default != nil}
	}
	return fields, true
}

// FormatCall is call, of `_format` or `_appendFormat` at a bound type, as
// the call of that type's format or appendFormat it stands for: the value,
// the builder for an appendFormat, then each field of the arguments passed
// to the parameter of its name -- the field's value where the arguments are a
// literal, a read of the field otherwise. A type declaring a format and no
// appendFormat is appended as the string its format writes,
// `out.add(x.format(…))`. ok is false for any other call, and for one whose
// type is not yet bound.
func FormatCall(scope *Scope, call *Call) (Expr, bool) {
	if call == nil || call.Func == nil {
		return nil, false
	}
	switch {
	case call.Func.Builtin == BuiltinFormatWith && len(call.Args) == 2:
		return formatCall(scope, call, "format", call.Args[0].Value, nil, call.Args[1].Value)
	case call.Func.Builtin == BuiltinAppendFormatWith && len(call.Args) == 3:
		x, out, pack := call.Args[0].Value, call.Args[1].Value, call.Args[2].Value
		if e, ok := formatCall(scope, call, "appendFormat", x, out, pack); ok {
			return e, true
		}
		text, ok := formatCall(scope, &Call{AST: call.AST, Type: TypString}, "format", x, nil, pack)
		if !ok {
			return nil, false
		}
		b := out.ExprType()
		if b != nil && b.Kind == TypeRef && len(b.Elems) == 1 {
			out = &Unary{Type: b.Elems[0], Op: ast.UnaryDeref, Operand: out}
			b = b.Elems[0]
		}
		add, _, ok := ResolveMethod(scope, b, "add")
		if !ok || len(add.Params) == 0 {
			return nil, false
		}
		return &Call{
			AST:         call.AST,
			Type:        call.Type,
			Func:        add,
			Args:        []CallArg{{Value: out}, {Name: add.Params[0].Name, Value: text}},
			ReceiverArg: true,
		}, true
	}
	return nil, false
}

// formatCall is the call of x's method, format or appendFormat, that call
// stands for, appending to out for an appendFormat, with pack's fields for
// its arguments.
func formatCall(scope *Scope, call *Call, method string, x, out, pack Expr) (Expr, bool) {
	t := x.ExprType()
	if t == nil || t.HasTypeParam() {
		return nil, false
	}
	fn, params, ok := FormatParams(scope, t, method)
	if !ok {
		return nil, false
	}
	args := make([]CallArg, 0, 2+len(params))
	args = append(args, CallArg{Value: x})
	if out != nil {
		// The builder, named as the checker names every argument after the
		// receiver: by the parameter it fills, the one before params.
		args = append(args, CallArg{Name: fn.Params[len(fn.Params)-len(params)-1].Name, Value: out})
	}
	lit, _ := pack.(*StructLit)
	for _, p := range params {
		var v Expr
		if lit != nil {
			// A literal written where the type was a parameter holds what
			// its constraint's format takes, and leaves the rest to the
			// format's own defaults.
			if v = lit.Field(p.Name); v == nil {
				continue
			}
		}
		if v == nil {
			v = &Select{Type: p.Type, Operand: CloneExprSharingDecls(pack), Field: p.Name}
		}
		args = append(args, CallArg{Name: p.Name, Value: v})
	}
	e := &Call{AST: call.AST, Type: call.Type, Func: fn, Args: args}
	if fn.RecvParam != nil || len(fn.RecvTypeParams) > 0 {
		e.ReceiverArg = true
	}
	return e, true
}

// NewDefaultFormat is the format the compiler declares for a value of t, an
// enum's or a unit's: `const func T.format(x T) string`, whose body is the
// text the target writes for one -- a member's name, a magnitude per base.
// It is held on the declaration rather than in its member table, so a format
// the program declares is the one found and is no duplicate of it.
func NewDefaultFormat(t *Type, pkg *Package) *Func {
	x := &Param{Name: "x", Type: t, Receiver: true}
	return &Func{
		Name: "format", Pkg: pkg,
		Receiver: t,
		Params:   []*Param{x},
		Return:   TypString,
		Block:    []Stmt{&Return{Value: &Conversion{Type: TypString, Operand: &Ident{Name: x.Name, Sym: x, Type: t}}}},
		Purity:   PurityPure,
		Const:    true,
	}
}

// DefaultFormat is the format the compiler declared for t (NewDefaultFormat):
// an enum's none of whose members carries a payload, or a unit's. Nil for any
// other type.
func DefaultFormat(t *Type) *Func {
	if t == nil {
		return nil
	}
	switch d := t.Decl.(type) {
	case *EnumDef:
		if !d.Carries() && len(t.Elems) == 0 {
			return d.Format
		}
	case *UnitDef:
		return d.Format
	}
	return nil
}

// IsDefaultFormat reports whether fn is a format the compiler declared for an
// enum or a unit.
func IsDefaultFormat(fn *Func) bool {
	return fn != nil && fn.Receiver != nil && DefaultFormat(fn.Receiver) == fn
}
