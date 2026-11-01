package checker

import (
	"fmt"
	"maps"
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// A type parameter's constraint says what a type bound to it must be, and is
// one of sngl:builtin's constraints: `any` says nothing, `anyStruct` asks for
// a struct, `anyFields` for a type with fields, `fieldsOf[S]` for one with at
// least S's fields at equal types, and `methodsOf[S]` for one with a method
// fitting each of S's functions. A named struct satisfies `fieldsOf` as
// readily as an anonymous one -- the constraint is about fields, not
// identity -- and so does a component, and a field default on the type being
// measured plays no part. `methodsOf` asks for nothing else, so an int may
// satisfy one.
//
// S's field may name another type parameter of the same list, or the one it
// constrains: `[T any, U fieldsOf[struct { value T }]]`. It is measured with
// what the list's other parameters are bound to, and where one is not, the
// field of the type bound to U is what binds it (inferFromConstraints).

// resolveConstraint resolves what a type parameter's constraint names, a
// constraint sngl:builtin declares, found in scope as any declaration is so a
// program's own of the name shadows it. The list's parameters are in scope, so
// S's field may name one, which makes S an anonymous struct like any other
// naming a type parameter: declared un-interned (anonStructType), its fields
// meaning this list's parameters and no other. self is the parameter it
// constrains, which a function `methodsOf` names is called on.
func (c *checker) resolveConstraint(te ast.TypeExpr, self *ir.Type) *ir.Type {
	switch t := te.(type) {
	case *ast.NamedType:
		if con, ok := c.namedConstraint(t, self); ok {
			return con
		}
	case *ast.StructDef:
		if len(t.TypeParams) == 0 {
			switch {
			case len(t.Funcs()) > 0:
				c.error(t.Pos, "a struct is no constraint: `methodsOf[struct { … }]` asks for its functions")
			case len(t.Fields()) > 0:
				c.error(t.Pos, "a struct is no constraint: `fieldsOf[struct { … }]` asks for its fields")
			default:
				c.error(t.Pos, "a struct is no constraint: `anyStruct` asks for a struct, and `anyFields` for a type with fields")
			}
			return ir.TypAny
		}
	}
	c.error(*te.ExprPos(), "a type parameter's constraint is `any`, `anyStruct`, `anyFields`, `fieldsOf[S]` or `methodsOf[S]`")
	return ir.TypAny
}

// namedConstraint is the constraint t names, ok false where it names none.
func (c *checker) namedConstraint(t *ast.NamedType, self *ir.Type) (*ir.Type, bool) {
	if t.Package != "" {
		return nil, false
	}
	sym, ok := c.scope.Lookup(t.Name)
	if !ok {
		return nil, false
	}
	if typ, _, ok := scalarOf(sym); ok && typ == ir.TypAny {
		if len(t.TypeArgs) > 0 {
			c.error(t.Pos, "`any` takes no type arguments")
		}
		return typ, true
	}
	sd, _ := sym.(*ir.StructDef)
	if sd == nil || !sd.Builtin.IsConstraint() {
		return nil, false
	}
	con := &ir.Type{Kind: ir.TypeConstraint, Decl: sd}
	switch sd.Builtin {
	case ir.BuiltinAnyStruct, ir.BuiltinAnyFields:
		if len(t.TypeArgs) > 0 {
			c.error(t.Pos, "`%s` takes no type arguments", t.Name)
		}
		return con, true
	}
	var s *ast.StructDef
	if len(t.TypeArgs) == 1 {
		s, _ = t.TypeArgs[0].(*ast.StructDef)
	}
	if s == nil || len(s.TypeParams) > 0 {
		c.error(t.Pos, "`%s` takes an anonymous struct, `%s[struct { … }]`", t.Name, t.Name)
		return ir.TypAny, true
	}
	if sd.Builtin == ir.BuiltinFieldsOf {
		for _, f := range s.Funcs() {
			c.error(f.Pos, "`fieldsOf` asks for fields; `methodsOf` asks for functions")
		}
		for _, f := range s.Fields() {
			if f.Default != nil {
				c.error(f.Pos, "a constraint's field takes no default: a constraint says what a type has, not what it starts with")
			}
		}
		con.Elems = []*ir.Type{c.anonStructType(c.buildStructDef(s).Fields)}
		return con, true
	}
	for _, f := range s.Fields() {
		c.error(f.Pos, "`methodsOf` asks for functions; `fieldsOf` asks for fields")
	}
	con.Elems = []*ir.Type{c.constraintFuncs(s.Funcs(), self)}
	return con, true
}

// constraintFuncs is the anonymous struct of the functions funcs declare,
// each called on self. It is declared un-interned: two of the same functions
// constrain different parameters.
func (c *checker) constraintFuncs(funcs []*ast.FuncDef, self *ir.Type) *ir.Type {
	sd := &ir.StructDef{Pkg: c.declPkg(), Anon: true, Methods: map[string]*ir.Func{}}
	for _, f := range funcs {
		if funcHasBody(f) {
			c.error(f.Pos, "a constraint's function takes no body: a constraint says what a type has, not what it does")
		}
		if len(f.TypeParams) > 0 {
			c.error(f.Pos, "a constraint's function takes no type parameters")
			continue
		}
		if _, dup := sd.Methods[f.Name]; dup {
			c.error(f.Pos, "duplicate declaration of %q in a constraint", f.Name)
			continue
		}
		fn := c.buildFunc(f)
		// A default is a constant, which the type's method must default to
		// as well (methodUnsatisfied): a call through the constraint may
		// leave it out, and the method is what is called.
		for _, ap := range f.Params.Params {
			p := ir.ParamNamed(fn.Params, ap.Name)
			if ap.Default == nil || p == nil {
				continue
			}
			p.Default = c.checkExprAs(ap.Default, p.Type, defaultSlot(*ap.Default.ExprPos()))
			if !plainConstant(p.Default) {
				c.error(*ap.Default.ExprPos(), "a constraint's parameter defaults to a constant, a literal or an enum member: the type's method must default to the same")
			}
		}
		fn.Receiver = self
		fn.RecvParam = &ir.Param{Name: ir.ReceiverParam, Type: self, Receiver: true}
		if f.Const {
			fn.Purity = ir.PurityPure
		}
		if fn.Return == nil {
			fn.Return = ir.TypVoid
		}
		sd.Methods[f.Name] = fn
	}
	return sd.SymType()
}

// paramFields is the fields the constraint on t, a type parameter, asks a
// type to have, and the constraint; ok is false where t is none or its
// constraint asks for no fields. In a generic body these are what a select
// and a spread of a T read.
func paramFields(t *ir.Type) (fields []*ir.StructField, con *ir.Type, ok bool) {
	if t == nil || t.Kind != ir.TypeTypeParam {
		return nil, nil, false
	}
	sym, _ := t.Decl.(*ir.TypeParamSym)
	if sym == nil || !ir.ConstraintFielded(sym.Constraint) {
		return nil, nil, false
	}
	return ir.ConstraintFields(sym.Constraint), sym.Constraint, true
}

// unsatisfied says why t does not satisfy constraint, "" where it does. An
// invalid type says nothing to measure, and neither does a type parameter no
// declaration constrains -- one a substitution made rather than a scope -- so
// both pass. A type parameter is measured by its own constraint: a T
// constrained to more fields than a U asks for may be handed on to U.
//
// The functions `methodsOf` names are measured with bindings, which binds the
// list it constrains: a function's parameter may name any of it, the
// constrained parameter included.
func (c *checker) unsatisfied(t, constraint *ir.Type, bindings map[string]*ir.Type) string {
	kind := ir.ConstraintKind(constraint)
	if kind == ir.BuiltinAny || t == nil {
		return ""
	}
	switch t.Kind {
	case ir.TypeInvalid:
		return ""
	case ir.TypeTypeParam:
		sym, _ := t.Decl.(*ir.TypeParamSym)
		if sym == nil {
			return ""
		}
		switch {
		case kind == ir.BuiltinAnyStruct && ir.ConstraintKind(sym.Constraint) != ir.BuiltinAnyStruct:
			return "its constraint does not say it is a struct"
		case kind != ir.BuiltinMethodsOf && !ir.ConstraintFielded(sym.Constraint):
			return "its constraint says nothing about its fields"
		}
	case ir.TypeStruct:
		if t.Decl == nil {
			return ""
		}
	}
	switch kind {
	case ir.BuiltinMethodsOf:
		methods := ir.ConstraintMethods(constraint)
		for _, name := range slices.Sorted(maps.Keys(methods)) {
			if why := c.methodUnsatisfied(t, methods[name], bindings); why != "" {
				return why
			}
		}
		return ""
	case ir.BuiltinAnyStruct:
		if t.Kind == ir.TypeTypeParam {
			return ""
		}
		if sd, _ := t.Decl.(*ir.StructDef); t.Kind != ir.TypeStruct || sd == nil || sd.RecordOf != nil {
			return "it is no struct"
		}
		return ""
	}
	fields, ok := ir.FieldsOf(t)
	if !ok {
		return "it has no fields"
	}
	for _, w := range ir.ConstraintFields(constraint) {
		got := ir.FieldNamed(fields, w.Name)
		if got == nil {
			return fmt.Sprintf("it has no field %q", w.Name)
		}
		if !got.Type.Equal(w.Type) {
			return fmt.Sprintf("its field %q is %s, not %s", w.Name, got.Type, w.Type)
		}
	}
	return ""
}

// methodUnsatisfied says why t's method named as want is does not take a call
// of want's shape, "" where it does: each of want's parameters is one of the
// method's of that name at an equal type, the method's others each have a
// default, it gives what want gives, and it is const where want is. A type
// parameter's is its own constraint's function, measured alike.
func (c *checker) methodUnsatisfied(t *ir.Type, want *ir.Func, bindings map[string]*ir.Type) string {
	if _, _, ok := c.resolveMethod(t, want.Name); !ok {
		return fmt.Sprintf("it has no method %q", want.Name)
	}
	fn, params, result, _ := ir.MethodParams(c.scope, t, want.Name)
	for _, p := range want.Params {
		q := ir.ParamNamed(params, p.Name)
		if q == nil {
			return fmt.Sprintf("its %s takes no parameter %q", want.Name, p.Name)
		}
		if wt := p.Type.Substitute(bindings); !q.Type.Equal(wt) {
			return fmt.Sprintf("its %s's parameter %q is %s, not %s", want.Name, p.Name, q.Type, wt)
		}
	}
	for _, q := range params {
		p := ir.ParamNamed(want.Params, q.Name)
		if q.Default == nil && !ir.IsFormatArgs(q.Type) && (p == nil || p.Default != nil) {
			return fmt.Sprintf("its %s's parameter %q has no default", want.Name, q.Name)
		}
	}
	if wt := want.Return.Substitute(bindings); !result.Equal(wt) {
		return fmt.Sprintf("its %s gives %s, not %s", want.Name, result, wt)
	}
	if want.Const && !fn.Const {
		return fmt.Sprintf("its %s is not const", want.Name)
	}
	c.matchDefaults(t, want, fn)
	return ""
}

// matchDefaults records, for runDefaultMatches, each default of want's that
// fn, t's method, has to default to as well. A method's defaults are checked
// with its body, which may come after the call measuring it.
func (c *checker) matchDefaults(t *ir.Type, want, fn *ir.Func) {
	pos := c.measuredAt
	for _, p := range want.Params {
		q := ir.ParamNamed(fn.Params, p.Name)
		if p.Default == nil || q == nil || ir.IsConstraintMethod(fn) {
			continue
		}
		c.defaultMatches = append(c.defaultMatches, func() {
			if !ir.SameConstant(constantOf(p.Default), constantOf(q.Default)) {
				c.error(pos, "%s does not satisfy its constraint: its %s's parameter %q defaults to %s, not %s",
					t, want.Name, p.Name, defaultText(fn, q), defaultText(want, p))
			}
		})
	}
}

// defaultText spells p's default, a parameter of fn, as it was written.
func defaultText(fn *ir.Func, p *ir.Param) string {
	if fn.AST != nil {
		for _, ap := range fn.AST.Params.Params {
			if ap.Name == p.Name && ap.Default != nil {
				return parser.FormatExpr(ap.Default)
			}
		}
	}
	return "something else"
}

// runDefaultMatches judges what matchDefaults recorded.
func (c *checker) runDefaultMatches() {
	for _, match := range c.defaultMatches {
		match()
	}
	c.defaultMatches = nil
}

// plainConstant reports whether e is a constant SameConstant can compare: a
// literal, null, an enum member, a conversion of one, a struct literal of
// them, or a const naming one.
func plainConstant(e ir.Expr) bool {
	e = constantOf(e)
	if ir.IsNullLiteral(e) {
		return true
	}
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		return x.Member != ""
	case *ir.Conversion:
		return plainConstant(x.Operand)
	case *ir.StructLit:
		for _, f := range x.Fields {
			if f.Spread || !plainConstant(f.Value) {
				return false
			}
		}
		return x.Def != nil
	}
	return false
}

// constantOf is e with each const it names, through any chain of them, its
// value: `base int = DEFAULT_BASE` defaults to what DEFAULT_BASE is.
func constantOf(e ir.Expr) ir.Expr {
	for range 64 {
		id, ok := e.(*ir.Ident)
		if !ok {
			return e
		}
		v, ok := id.Sym.(*ir.Var)
		if !ok || !v.IsConst || v.Init == nil {
			return e
		}
		e = v.Init
	}
	return e
}

// inferFromConstraints binds the parameters of params a constraint's fields
// name from the fields of the type bound to the parameter it constrains:
// `[T any, U fieldsOf[struct { value T }]]` with U a struct whose value is an int binds
// T to int. Repeated until nothing more binds, since what one binds may be
// what another's constraint is measured against. Nothing already bound is
// rebound, and only params' own names are: a field may name a parameter of
// an enclosing declaration, which this call does not bind.
func inferFromConstraints(params []ir.TypeParam, bindings map[string]*ir.Type) {
	own := make(map[string]bool, len(params))
	for _, tp := range params {
		own[tp.Name] = true
	}
	for changed := true; changed; {
		changed = false
		for _, tp := range params {
			want := ir.ConstraintFields(tp.Constraint)
			t := bindings[tp.Name]
			if want == nil || t == nil {
				continue
			}
			fields, ok := ir.FieldsOf(t)
			if !ok {
				continue
			}
			for _, w := range want {
				got := ir.FieldNamed(fields, w.Name)
				if got == nil || !w.Type.HasTypeParam() {
					continue
				}
				found := map[string]*ir.Type{}
				ir.BindTypeParams(w.Type, got.Type, found)
				for name, b := range found {
					if _, bound := bindings[name]; !bound && own[name] && b.Kind != ir.TypeInvalid {
						bindings[name] = b
						changed = true
					}
				}
			}
		}
	}
}

// checkConstraint reports at pos a type bound to tp that does not satisfy its
// constraint, measured with the list's other parameters bound as bindings
// says. Every place a type parameter is bound asks this.
func (c *checker) checkConstraint(pos ast.Pos, tp ir.TypeParam, t *ir.Type, bindings map[string]*ir.Type) {
	con := tp.Constraint.Substitute(bindings)
	c.measuredAt = pos
	if why := c.unsatisfied(t, con, bindings); why != "" {
		c.error(pos, "%s does not satisfy %s, the constraint on %s: %s", t, con, tp.Name, why)
	}
}

// checkConstraints is checkConstraint for each of params bindings binds,
// once what the constraints themselves bind has been bound.
func (c *checker) checkConstraints(pos ast.Pos, params []ir.TypeParam, bindings map[string]*ir.Type) {
	inferFromConstraints(params, bindings)
	for _, tp := range params {
		if t, ok := bindings[tp.Name]; ok {
			c.checkConstraint(pos, tp, t, bindings)
		}
	}
}
